<!-- SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# mofgw — Guía de usuario

Proxy de IA self-hosted, minimalista y en Go. **Un solo endpoint OpenAI-compatible** y **fallback transparente entre providers**: el cliente apunta a un único lugar y mofgw rota internamente si un provider falla (429, 5xx, timeout, 400 max_tokens) sin que el cliente se entere.

**Estado actual:** ✅ MVP (EPIC-001) + EPIC-002 (resiliencia) + EPIC-004 (escala) + EPIC-005 (ops) implementados y verificados con smoke tests E2E. Suite de 83+ tests verde con `-race`.

---

## Qué es y para qué sirve

mofgw expone un endpoint OpenAI-compatible (`POST /v1/chat/completions`) y, por detrás, mantiene una **cadena de providers upstream** configurable. Si el primer provider no responde (o responde mal), mofgw prueba el siguiente en silencio.

**Principio rector:** el cliente nunca se entera. El proxy es **transparente**: reenvía el body crudo, no interpreta ni modifica tools/parámetros. No hay política de strip de tools — si el cliente manda `tools[]` a un modelo que no las soporta, el error es del modelo, no del proxy (decisión de diseño, 05 Ago 2026).

Casos de uso:

- **Agentes clientes** (OpenClaw, crons, sesiones aisladas) apuntan a un solo endpoint; mofgw resuelve qué provider usar.
- Reemplaza a un gateway previo en Node (~1GB RAM, 55s de arranque): mofgw es un binario Go ligero.
- Un solo lugar donde viven las API keys de providers (nunca en los clientes).

## Arquitectura en una línea

```
cliente (OpenClaw / cron / sesión aislada)
        │  https://… o http://127.0.0.1:3369/v1  (Bearer: key de cliente)
        ▼
   mofgw:3369  ──►  providers upstream, en orden de config:
                     1. provider-a   (OpenAI-compatible)
                     2. provider-b   (OpenAI-compatible)
                     3. provider-c   (OpenAI-compatible)
                     …
```

El cliente solo ve el endpoint de mofgw. Si el provider 1 falla, mofgw prueba el 2, luego el 3… y solo si **todos** fallan el cliente ve un error — en formato OpenAI-compatible y sin detalles internos.

## Instalación y arranque

### Build

```bash
cd <checkout-del-repo>   # o tu checkout del módulo github.com/ofapsaas/mofgw
go build -o mofgw ./cmd/mofgw
```

### Config

mofgw busca el config en este orden de precedencia:

1. **Flag explícito:** `mofgw -config /ruta/al/config.yaml`
2. **Nivel home:** `~/.config/mofgw/config.yaml`
3. **Nivel sistema:** `/etc/mofgw/config.yaml`

Si no encuentra ninguno, el arranque falla con error descriptivo. Un config inválido (sin providers, ids duplicados, env var no seteada, hash malformado…) también falla al arrancar — **fail fast**.

### Arranque

```bash
./mofgw
# o con config explícito:
./mofgw -config /ruta/al/config.yaml
# o con debug + log a archivo:
./mofgw --verbose --log-file /var/log/mofgw.log
```

SIGINT/SIGTERM hacen shutdown limpio (10s de gracia). El binario loguea con `slog` (`config cargado`, `mofgw escuchando`, `provider en cooldown`, …).

> **Nota:** el deploy con systemd user (005-003) **YA está implementado**: `./scripts/install.sh` instala mofgw como servicio de usuario (`systemctl --user`). Flags `--verbose`/`--log-file` disponibles, config en `~/.config/mofgw/config.yaml`, keys por env file. Ver «Deploy con systemd user» abajo.

### Deploy con systemd user

```bash
./scripts/install.sh               # instala y arranca como servicio de usuario (idempotente)
./scripts/install.sh --uninstall   # detiene, desinstala y limpia
```

- **Idempotente:** re-ejecutarlo no duplica units ni pisa la config existente.
- El unit corre **como tu usuario** (`systemctl --user`), sin root.
- **Logs a journald:** `journalctl --user -u mofgw -f`.
- **Reboot persistence:** habilita **linger** para que el servicio sobreviva sin sesión abierta: `loginctl enable-linger <usuario>` (puede requerir permisos/root).

### Flags

| Flag | Descripción |
|------|-------------|
| `-config <path>` | Path explícito al `config.yaml` (precedencia en la sección Config). |
| `--verbose` | Nivel debug: qué recibe/envía/decide el proxy. Emite eventos `request_start`, `request_end`, `provider_attempt`, `provider_fallback`, `decision`, `cooldown_start`. Solo metadatos, nunca contenido. |
| `--log-file <path>` | Archivo de log (append). Con `--verbose` el debug va **solo** al archivo. |

**Topología de destinos:**

| Nivel | Sin `--log-file` | Con `--log-file` |
|-------|------------------|------------------|
| info (default) | stdout | stdout + archivo |
| `--verbose` (debug) | stdout | **solo archivo** (stdout queda limpio con info/errores) |

Es decir: `--log-file` no cambia el destino de info (siempre stdout, y además archivo); lo que cambia es verbose — sin archivo va a stdout, con archivo va solo al archivo. Ejemplo:

```bash
./mofgw --verbose --log-file /var/log/mofgw.log
# → info a stdout + /var/log/mofgw.log; debug SOLO a /var/log/mofgw.log
```

## Configuración (`config.yaml`)

Copia `config.example.yaml` a `~/.config/mofgw/config.yaml` y edítalo. Estructura general:

```yaml
server:    # listener HTTP
fallback:  # política global de la cadena
providers: # lista ordenada de upstreams (¡el orden ES la cadena!)
clients:   # clientes autorizados (agentes)
```

### `server`

| Campo | Default | Descripción |
|-------|---------|-------------|
| `addr` | `127.0.0.1:3369` | Bind. Loopback por defecto (auth de todos modos). |
| `max_body_bytes` | `10485760` (10MB) | Límite del body; excederlo devuelve 413. |
| `read_timeout` | `120s` | Timeout de lectura HTTP. |
| `write_timeout` | `300s` | Timeout de escritura HTTP. |

### `fallback`

| Campo | Default | Descripción |
|-------|---------|-------------|
| `max_retries` | `2` | Tope de intentos **totales** por request con recorrido **circular** de la cadena (`maxAttempts = max_retries + 1`). Con N providers, `max_retries >= N-1` garantiza recorrer toda la cadena en un solo request; valores mayores dan vueltas adicionales. |
| `cooldown` | `60s` | Tiempo que un provider queda "enfriado" tras un fallo retryable (no se le vuelve a intentar hasta que pase). |
| `cooldown_jitter` | `5s` | ±jitter aleatorio sobre el cooldown (anti thundering-herd: varios requests no golpean al mismo provider a la vez). |
| `timeout` | `120s` | Timeout global por intento (TTFB). |

### `providers` — el orden ES la cadena de fallback

Cada provider:

| Campo | Obligatorio | Descripción |
|-------|-------------|-------------|
| `id` | ✅ | Identificador único (ej: `provider-a`). |
| `base_url` | ✅ | URL base del upstream OpenAI-compatible (ej: `https://api.openai.com/v1`). |
| `api_key_env` | ✅ | **Nombre de la variable de entorno** con la key. **Las keys NUNCA van en el YAML** — se resuelven al cargar; si la env var no está seteada, el arranque falla. |
| `models` | ✅ | Lista de modelos que sirve este provider (al menos 1). |
| `max_tokens` | — | Límite de `max_tokens` del provider. El proxy **clampea** el body por intento contra este límite. |
| `cooldown` | — | Override del cooldown global (opcional). |
| `timeout` | — | Override del timeout global (opcional). |

Semántica de `cooldown`/`timeout` por provider (3 estados):

- **ausente (nil)** → usa el valor global de `fallback`
- **0 explícito** → sin límite (el intento puede durar lo que tarde)
- **N (positivo)** → N para ese provider

```yaml
providers:
  - id: provider-a
    base_url: "https://api.provider-a.example.com/v1"
    api_key_env: MOFGW_PROVIDER_A_KEY
    models: ["deepseek-v4-flash", "deepseek-v4-pro"]
    max_tokens: 8192
    # cooldown: 90s    # override por provider (opcional)
    # timeout: 60s     # override por provider (opcional)
  - id: provider-b
    base_url: "https://api.provider-b.example.com/compatible-mode/v1"
    api_key_env: MOFGW_PROVIDER_B_KEY
    models: ["qwen3.7-plus", "glm-5.2"]
    max_tokens: 65536
```

> **Importante:** el orden de la lista define la cadena. Si quieres que `provider-b` se intente antes que `provider-a`, mueve su bloque arriba. Editar el orden **ES** la configuración del fallback.

### `clients` — autenticación

Cada cliente autorizado (agente, cron…) tiene su propia API key. En el config **solo va el hash SHA-256 hex** de la key (`key_sha256`), nunca la key en claro.

```yaml
clients:
  - id: agent-main
    key_sha256: "<sha256 hex de la key del agente principal>"
  - id: cron-daily
    key_sha256: "<sha256 hex de la key del cron>"
```

Genera el hash con el binario (la key nunca se loguea):

```bash
mofgw hash-key <tu-key-super-secreta>
# o leyendo de stdin (útil para pipes):
echo -n <tu-key> | mofgw hash-key
```

### Ejemplo completo

```yaml
server:
  addr: "127.0.0.1:3369"      # bind loopback por defecto (auth de todos modos)
  max_body_bytes: 10485760     # 10MB
  read_timeout: 120s
  write_timeout: 300s

fallback:
  max_retries: 2               # tope de intentos totales por request (recorrido circular) = max_retries + 1
  cooldown: 60s                # cooldown global por provider tras fallo retryable
  cooldown_jitter: 5s          # ± jitter anti thundering-herd
  timeout: 120s                # timeout global por intento (TTFB)

providers:
  - id: provider-a
    base_url: "https://api.provider-a.example.com/v1"
    api_key_env: MOFGW_PROVIDER_A_KEY
    models: ["deepseek-v4-flash", "deepseek-v4-pro"]
    max_tokens: 8192
  - id: provider-b
    base_url: "https://api.provider-b.example.com/compatible-mode/v1"
    api_key_env: MOFGW_PROVIDER_B_KEY
    models: ["qwen3.7-plus", "glm-5.2"]
    max_tokens: 65536

clients:
  - id: agent-main
    key_sha256: "<sha256 hex de la key del agente principal>"
  - id: cron-daily
    key_sha256: "<sha256 hex de la key del cron>"
```

## Endpoints

Todos los de `/v1/*` exigen auth Bearer. `/healthz` y `/metrics` son públicos (monitoreo local, bind default loopback). Las rutas no declaradas devuelven 404 OpenAI-compatible.

### `POST /v1/chat/completions` (auth)

Endpoint principal. Acepta **no-stream** y **stream** (SSE), y cualquier campo del contrato OpenAI (`tools`, `top_p`, `stream_options`…): el body viaja **crudo**, nada se pierde ni se modifica en el round-trip.

> **Nota (05 Ago 2026, hotfix 001-001-endpoint-fix-content-array):** el proxy acepta `messages[].content` como **string** O como **array de partes** (formato OpenAI multimodal/reasoning, ej. `[{"type":"text","text":"hola"}]`). El parseo de ruteo es transparente — no interpreta la forma del contenido — así que ninguna forma se rechaza con 400; el body se reenvía crudo al upstream.

- Body sin `model` o sin `messages` → `400 invalid_request_error`.
- Body mayor a `server.max_body_bytes` → `413 invalid_request_error`.
- Modelo que ningún provider sirve → `404 model_not_found` (sin intentos de red).
- Todos los providers en cooldown → `503 upstream_error`.
- Todos los intentos fallaron → `502 upstream_error`.

### `GET /v1/models` (auth)

Lista los modelos de **todos** los providers configurados, deduplicados, en formato OpenAI (`object: "list"`, cada ítem `object: "model"`, `owned_by: "mofgw"`).

### `GET /healthz` (sin auth)

Healthcheck simple. Responde `{"status":"ok"}`.

### `GET /metrics` (sin auth)

Contadores en texto plano estilo Prometheus (prefijo `mofgw_`):

```
mofgw_requests_total
mofgw_streams_total
mofgw_errors_total
mofgw_failovers_total
mofgw_cooldown_hits_total
mofgw_last_provider   # solo si hubo al menos una respuesta
```

Desde 003-001 (instrumentación de cache de providers) también:

```
mofgw_cache_hit_tokens_total      # tokens servidos del cache de prompt del provider
mofgw_cache_miss_tokens_total     # tokens procesados frescos (prompt - cached)
mofgw_reasoning_tokens_total      # tokens de razonamiento (thinking), facturados como output
mofgw_cache_tokens{provider="...",model="..."} hit=.. miss=.. reasoning=..   # desagregado
```

Desde 006-001 (accounting por cliente):

```
mofgw_prompt_tokens_total         # tokens de entrada acumulados (todos los clientes)
mofgw_completion_tokens_total     # tokens de salida acumulados
mofgw_total_tokens_total          # prompt + completion acumulados
mofgw_usage_tokens{client="...",provider="...",model="..."} prompt=.. completion=.. total=..   # por cliente
```

Desde 006-002 (costo estimado):

```
mofgw_cost_usd_total              # costo estimado acumulado en USD
mofgw_cost_usd{client="...",provider="...",model="..."} <usd>   # por cliente
```

> El costo se calcula en el gateway con la tabla de precios configurable
> (por millón de tokens: input, output, cache-hit) — es una **estimación**
> para visibilidad de gasto; la factura real la emite cada provider.

> El cache de prompt es **automático** del lado del provider (DeepSeek,
> Alibaba, MiniMax, GLM, Kimi y el gateway zen lo activan sin opt-in). En
> streaming, mofgw garantiza `stream_options.include_usage=true` para que
> el chunk final traiga el usage con los campos de cache. El accounting por
> cliente usa el clientID autenticado (001-007) — la base para costos
> (006-002) y budgets (EPIC-008).

### Autenticación

```http
Authorization: Bearer <client-key>
```

El proxy calcula el SHA-256 de la key enviada y lo compara (constant-time) contra los `key_sha256` del config. Si falta o no coincide → `401` con tipo de error `invalid_api_key`.

## Uso

### Con curl — chat no-stream

```bash
curl -sS http://127.0.0.1:3369/v1/chat/completions \
  -H "Authorization: Bearer <client-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-v4-flash",
    "messages": [{"role": "user", "content": "Hola, ¿quién eres?"}],
    "max_tokens": 512
  }'
```

### Con curl — stream (SSE)

```bash
curl -sSN http://127.0.0.1:3369/v1/chat/completions \
  -H "Authorization: Bearer <client-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-v4-flash",
    "messages": [{"role": "user", "content": "Cuenta un chiste corto"}],
    "stream": true
  }'
```

Verás `data: {…}` por evento y el terminador `data: [DONE]`. El `model` de cada evento se reescribe al que pediste y el `id` es estable (`mofgw-*`).

### Con curl — modelos y health

```bash
curl -sS http://127.0.0.1:3369/v1/models \
  -H "Authorization: Bearer <client-key>"
curl -sS http://127.0.0.1:3369/healthz      # → {"status":"ok"}
```

### Con un cliente CLI

Cualquier cliente OpenAI-compatible sirve. Ejemplo con `curl` para no-stream, o con un CLI de terminal:

```bash
# curl (no-stream) — ver ejemplos arriba
# CLI genérico estilo openai:
your-cli -p "tu prompt" \
  --model <modelo> \
  --base-url http://127.0.0.1:3369/v1 \
  --api-key <client-key>
```

### Generar una key de cliente

```bash
mofgw hash-key <key>
# y copiar el hash a clients[].key_sha256 del config
```

## Semántica del fallback (transparencia)

### Cómo funciona la cadena

1. mofgw parsea el body solo para **rutear**: `model`, `stream`, `max_tokens` (el resto viaja crudo).
2. Toma los providers que **sirven ese modelo** (su lista `models`), en orden de config.
3. Intenta el primero. Si responde bien, **esa** es la respuesta del cliente (el modelo del body de respuesta se reescribe al que pidió el cliente).
4. Si el intento falla con error **retryable**, registra cooldown para ese provider (con ±jitter) y prueba el siguiente.
5. **Recorrido circular:** `max_retries` es un tope de intentos **totales** por request (`maxAttempts = max_retries + 1`), no una sola pasada de la cadena. Al llegar al último provider, el loop **vuelve al primero** y sigue probando hasta agotar los intentos. Con N providers que sirven el modelo y `max_retries >= N-1`, el proxy agota **toda la cadena en un solo request**; valores mayores dan vueltas adicionales completas.
6. **El cooldown NO filtra dentro del mismo request:** un provider que falló en este request puede volver a intentarse en la siguiente vuelta del loop. El cooldown registrado afecta **solo a requests posteriores** — un provider en cooldown se salta en requests nuevos, pero el loop intra-request reintenta.
7. Solo si se agotan **todos** los intentos (o ninguno sirve el modelo) el cliente ve un error OpenAI-compatible, **sin detalles internos**: los mensajes de error upstream se sanear (URLs, tokens, paths redactados).

**Ejemplo:** con 5 providers y `max_retries: 9` → hasta 10 intentos = 2 vueltas completas a la cadena. Si el primer provider está caído, el proxy prueba los demás en el **mismo request** hasta encontrar uno vivo, sin depender del retry del cliente.

### Errores retryable vs no-retryable

| Clasificación | Condición | Acción |
|---------------|-----------|--------|
| **Retryable** | `429` (rate limit), `5xx`, timeout (TTFB), error de red | Cooldown del provider + probar el siguiente (recorrido circular: puede reintentar en la misma request) |
| **No-retryable** | 4xx de cliente (400 inválido, 401, 403…) | Responder al cliente ya — **no** hay fallback |

Los `401/403` de cliente no disparan fallback: son problemas del request, no del provider.

### Streaming: la frontera del primer byte

- **Antes del primer byte:** el router bufferiza el primer evento. Si el intento falla (429, 5xx, timeout, red), lo descarta silenciosamente y prueba el siguiente provider. El cliente solo ve el primer intento que produce un primer byte.
- **Después del primer byte:** el stream está **comprometido**. No hay reconexión a otro provider. Si el upstream muere a mitad, mofgw propaga un **error SSE limpio** (formato OpenAI-compatible) seguido de `[DONE]`.

### Clamp de `max_tokens`

Por cada intento, mofgw clampea el `max_tokens` del body contra el límite del provider destino (`providers[].max_tokens`). Un body que ya cumple o un provider sin límite pasan intactos.

## Limitaciones conocidas

- **Modelos sin soporte de tools:** algunos modelos (ej. `smollm:135m` y similares) devuelven `502` si el cliente manda `tools[]` en el request (algunos clientes siempre lo hacen). Es **comportamiento correcto** del proxy transparente — no se strippean tools (decisión de diseño). Usa un modelo con soporte de tools para esos clientes.
- **`401/403` de cliente no disparan fallback** (ver tabla arriba).
- **Cooldown efímero en memoria:** al reiniciar mofgw, todos los providers arrancan "fríos".
- **Métricas mínimas:** el `/metrics` actual son contadores simples; el formato Prometheus completo es roadmap.
- **Sin health checks periódicos:** el estado de los providers se descubre por intentos reales, no por sondeos.
- **Privacidad de logs:** aun con `--verbose`, **nunca** se loguea contenido de prompts, respuestas LLM, definiciones de tools ni API keys — es por diseño (privacidad). Los logs de debug son solo metadatos (modelo, counts, status, duraciones, causas).

## Roadmap (lo que NO está implementado todavía)

- **Retry con backoff exponencial** (EPIC-002 / 002-001).
- **Health checks periódicos** (EPIC-002 / 002-002).
- **Métricas Prometheus completas** (005-001, parcial: ya existen contadores mínimos).
- **Cache LLM + eficiencia** (EPIC-003) y **escala multi-agente / multi-tenancy** (EPIC-004).

---

*Docs del proyecto: [`README.md`](../README.md) · [`docs/epics/plan.md`](epics/plan.md) · specs en [`docs/specs/`](specs/).*
