<!-- SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# mofgw — Guía de usuario

Proxy de IA self-hosted, minimalista y en Go. **Un solo endpoint OpenAI-compatible** y **fallback transparente entre providers**: el cliente apunta a un único lugar y mofgw rota internamente si un provider falla (429, 5xx, timeout, 400 max_tokens) sin que el cliente se entere.

**Estado actual:** ✅ Programa completo implementado y desplegado — EPIC-001/002/004/005 + replanteo de eficiencia (003/006/007/008: cache providers, medición de tokens/costos, catálogo de capabilities, contexto) + hardening de seguridad (SEC-001). Suite **14 paquetes, 210+ tests** verde con `-race`.

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
| `state_file` | *(vacío)* | Ruta del snapshot JSON de accounting (usage/cost/sesiones). Vacío = persistencia deshabilitada (todo en memoria). Si existe al arrancar se restaura; corrupto → warn + estado limpio (nunca bloquea). |
| `state_save_interval` | `0` | Cada cuánto se persiste en caliente. `0` = solo al shutdown limpio. Se ignora si `state_file` está vacío. |

### `fallback`

| Campo | Default | Descripción |
|-------|---------|-------------|
| `max_retries` | `2` | Tope de intentos **totales** por request con recorrido **circular** de la cadena (`maxAttempts = max_retries + 1`). Con N providers, `max_retries >= N-1` garantiza recorrer toda la cadena en un solo request; valores mayores dan vueltas adicionales. |
| `cooldown` | `60s` | Tiempo que un provider queda "enfriado" tras un fallo retryable (no se le vuelve a intentar hasta que pase). |
| `cooldown_jitter` | `5s` | ±jitter aleatorio sobre el cooldown (anti thundering-herd: varios requests no golpean al mismo provider a la vez). |
| `inter_attempt_delay` | `0` | Retardo (015-001) que se duerme **antes** del siguiente intento de la cadena cuando el provider que falló y el siguiente candidato **comparten `base_url`** (cuentas del mismo proveedor = SPOF). Da tiempo al endpoint a recuperarse tras un blip transitorio (connect/network/timeout/I-O/EOF pre-primer-byte). **NO** aplica en `429` (cuota: salto inmediato). Es `ctx`-aware (aborta si el cliente cancela). `0` = off (cero regresión). Ver §Limitaciones para más detalle. |
| `timeout` | `120s` | Timeout global por intento (TTFB). |

### `providers` — el orden ES la cadena de fallback

Cada provider:

| Campo | Obligatorio | Descripción |
|-------|-------------|-------------|
| `id` | ✅ | Identificador único (ej: `provider-a`). |
| `base_url` | ✅ | URL base del upstream OpenAI-compatible (ej: `https://api.openai.com/v1`). |
| `api_key_env` | ✅ | **Nombre de la variable de entorno** con la key. **Las keys NUNCA van en el YAML** — se resuelven al cargar; si la env var no está seteada, el arranque falla. |
| `models` | ✅ | Lista de modelos que sirve este provider (al menos 1). |
| `max_tokens` | — | Límite de `max_tokens` del provider. El proxy **clampea** el body por intento contra este límite (efectivo = `min(max_output_modelo, max_tokens_provider)`). |
| `thinking_path` | — | Path de inyección del `thinking_default` (010-002). Valores: `""` (default, sin inyección) \| `"zen"` \| `"bailian"`. `"zen"` = passthrough de `reasoning_effort`; `"bailian"` = `reasoning_effort` + `enable_thinking` (modelos de razonamiento) o `enable_thinking` solo (modelos tipo qwen). Modelos de thinking siempre-activo o con default nativo == prescriptivo nunca se inyectan. |
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
    models: ["model-a", "model-b"]
    max_tokens: 8192
    # thinking_path: "zen"   # inyección del thinking_default (010-002)
    # cooldown: 90s    # override por provider (opcional)
    # timeout: 60s     # override por provider (opcional)
  - id: provider-b
    base_url: "https://api.provider-b.example.com/compatible-mode/v1"
    api_key_env: MOFGW_PROVIDER_B_KEY
    models: ["vision-model"]
    max_tokens: 131072
    thinking_path: "bailian"
```

> **Importante:** el orden de la lista define la cadena. Si quieres que `provider-b` se intente antes que `provider-a`, mueve su bloque arriba. Editar el orden **ES** la configuración del fallback.

### `providers` de tipo `subprocess` (EPIC-013) — CLI claude / suscripciones

Un provider `type: subprocess` ejecuta el CLI de un backend de IA como subproceso en vez de hablar HTTP. Permite usar una **suscripción de consumidor** (p.ej. Claude Pro) sin API key ni reverse-engineering de OAuth: mofgw corre el binario `claude` y traduce entre OpenAI y el formato del CLI.

Campos específicos (además de `id`, `models`, `max_tokens`, `clients`):

| Campo | Obligatorio | Descripción |
|-------|-------------|-------------|
| `type` | ✅ | `"subprocess"` para este tipo. Ausente o `"http"` = provider HTTP (default). |
| `backend` | ✅ | Backend del motor: `"claude"` hoy (gemini/codex a futuro). `!= "claude"` → error de carga. |
| `command` | — | Binario del CLI (default `"claude"`). **Recomendado ruta absoluta** — el PATH del servicio systemd no siempre incluye `~/.local/bin`. |
| `session_dir` | — | Base de sesiones por cliente (default `~/.config/mofgw/sessions`). |
| `backend_flags` | — | Flags opacos que se agregan al argv del CLI (control de tools/permisos, ver abajo). |
| `clients` | — | **Allowlist de clientID** con acceso a este provider (013-003). Vacío/ausente = **sin restricción** (cualquier cliente autenticado puede usarlo). Solo agentes del usuario recomendado. |

```yaml
providers:
  - id: claude-pro
    type: subprocess
    backend: claude
    command: "$HOME/.local/bin/claude"          # ruta absoluta (PATH del service)
    models: ["claude-sonnet-4-6", "claude-haiku-4-5", "claude-opus-4-7"]
    max_tokens: 8192
    # backend_flags: ["--permission-mode", "strict", "--disallowedTools", "Bash,Edit,Write"]
    clients: ["agent-main", "cron-daily"]     # SOLO estos clientes pueden usarlo
```

**Comportamiento y limitaciones (verificado con el CLI real 2.1.229):**

- **Sesiones nombradas por cliente**: la primera vez que un cliente usa el provider, mofgw crea la sesión (`-n <clientID>`); las siguientes la reanudan (`--resume <clientID>`), reutilizando el prompt-cache (~8x más barato). mofgw envía solo el último mensaje `user` por stdin; el CLI mantiene el historial.
- **Sin `base_url`/`api_key_env`**: el subprocess NO usa API key (claude usa OAuth/suscripción). Si el config loader te reclama esos campos para un `type: subprocess`, estás corriendo un **binario viejo** — recompilá con la fuente actual.
- **Modelos marcados sin tools**: el CLI usa sus propias tools por detrás; mofgw las omite del request (el modelo se ve como text-only). Declará en `model_metadata` con `supported_parameters: ["reasoning"]` (sin `"tools"`).
- **Continuación de sesión**: si `--resume` falla con "no conversation found" (dir stale), el motor reintenta con creación (`-n`). Si hay **duplicados** con el mismo nombre, `--resume` falla con "matches N sessions" — reparar con `scripts/claude-session-repair.sh <clientID> [--keep-newest|--fresh]`.
- **Control de tools/permisos** vía `backend_flags` (opacos): ej. read-only `["--permission-mode","strict","--disallowedTools","Bash,Edit,Write"]`, o allowlist `["--allowedTools","Read,WebSearch"]`. NO usar `--dangerously-skip-permissions` salvo riesgo asumido.
- **Costo**: el primer call de un cliente paga la creación de la cache del scaffolding del CLI (~$0.03-0.07); los siguientes son baratos. Es una suscripción, no pay-per-token: el `usage`/costo que reporta mofgw es **estimado** (del largo del output), no exacto.
- **Estabilidad**: la integración con el CLI es un *hack* — el CLI no es una API estable; el formato de salida varía según contexto (deltas vs mensaje completo). mofgw maneja ambos, pero no es tan robusto como un provider HTTP.

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
    models: ["model-a"]
    max_tokens: 8192
  - id: provider-b
    base_url: "https://api.provider-b.example.com/compatible-mode/v1"
    api_key_env: MOFGW_PROVIDER_B_KEY
    models: ["vision-model"]
    max_tokens: 131072
    thinking_path: "bailian"

clients:
  - id: agent-main
    key_sha256: "<sha256 hex de la key del agente principal>"
  - id: cron-daily
    key_sha256: "<sha256 hex de la key del cron>"
```

### `pricing` — costos por modelo (006-002)

Tabla de precios por **millón de tokens** (USD), keyed por modelo. Alimenta `mofgw_cost_usd_total` en `/metrics` y el campo `pricing` del catálogo `/v1/models`.

```yaml
pricing:
  model-a:
    input_usd_per_m: 0.14
    output_usd_per_m: 0.28
    cache_hit_usd_per_m: 0.0028
```

- Sin entrada para un modelo → costo 0 (no error, backward compatible).
- Precios negativos → error al cargar.
- **Nota:** la tabla es keyed por modelo y aplica a todos los providers que lo sirven. Si distintos providers cobran distinto por el mismo modelo, usa la tasa del provider por el que pasa la mayor parte del tráfico (la tasa real depende del endpoint de cada provider — verifica la del tuyo; `research-token-efficiency.md` §1 documenta cómo verificar).

### `model_metadata` — capabilities por modelo (007-001 / 010-001)

Metadata declarativa que alimenta `/v1/models` (context window, max output, niveles de thinking, tools, modalidad, default prescriptivo de razonamiento). Sin esto, los runtimes asumen defaults equivocados (opencode ve `reasoning:false, context:0`).

```yaml
model_metadata:
  model-a:                          # modelo de razonamiento con effort graduado
    context_window: 1048576
    max_output: 384000
    thinking: ["low", "high", "max"]
    thinking_default: "low"         # PRESCRIPTIVO (ADR-003): effort que el agente DEBE enviar
    supported_parameters: ["tools", "reasoning"]
    modality: "text->text"
  vision-model:                     # multimodal (texto + imagen + video)
    context_window: 1048576
    max_output: 131072
    thinking: ["adaptive", "disabled"]
    thinking_default: "adaptive"
    supported_parameters: ["tools", "reasoning"]
    modality: "text+image+video->text"
```

- `thinking_default` es **prescriptivo** (ADR-003): documenta el effort de razonamiento que los agentes DEBEN enviar y puede diferir del default nativo del modelo. El gateway lo **inyecta** cuando el cliente no especifica (010-002), y debe estar en `thinking` (si no → error al cargar).
- `supported_parameters` y `modality` son **declarativos**: si no se declaran, el campo se **omite** del catálogo (fallback rule — ausente ≠ 0/[]/{}). `modality` usa formato `"inputs->outputs"` (p. ej. `text+image->text`).
- Valores negativos → error al cargar.
- La inyección del default por-attempt y provider-aware se configura con el knob `providers[].thinking_path` (ver abajo). Niveles de thinking reales por modelo: `research-token-efficiency.md` §4.

### `context` — rechazo por ventana (008-001)

```yaml
context:
  margin: 0.1   # fracción 0-1 sobre context_window (default 0.1)
```

Un request cuyo prompt estimado (`len(body)/4`, estimación rough) excede `context_window × (1 + margin)` se rechaza con `400 invalid_request_error` **sin gastar intentos de la cadena**. Solo aplica si el modelo tiene `model_metadata.context_window`.

### `clients[].budget` — límite de consumo por cliente (008-002)

```yaml
clients:
  - id: agent-main
    key_sha256: "<sha256>"
    budget:
      cost_usd_max: 5.0        # 0 = sin límite
      tokens_max: 10000000     # 0 = sin límite
      window: 24h              # documentación (ventana simple desde el arranque)
```

Al exceder el budget, el cliente recibe `429 rate_limit_exceeded` sin tocar el upstream. El costo usa el mismo cálculo estimado de `pricing`.

### `registry` — registro unificado de accounting + outcome (014-001)

Registro **append-only (JSONL) en disco** del ciclo completo de cada request que pasa por la cadena de routing. Es la **fuente de verdad** para responder "¿cuánto gastó el cliente X en el provider Y en este período?" y "¿qué provider patinó y por qué?" — cosas que `/metrics` (acumulado desde arranque, sin ventana temporal) y el telemetry de descubrimiento (`sample_rate<1`, sin tokens/costo) **no** cubren.

```yaml
registry:
  enabled: false        # off por default — sin true no se crea ni escribe el archivo
  # file: "/var/log/mofgw/registry.jsonl"
```

- `enabled` + `file` **vacío** → error de validación al arrancar (el proceso no levanta).
- Emite, por cada request: un **evento por intento** (cada provider probado: `request_id`, `ts`, `client`, `provider`, `model`, `outcome ok|fallback`, `cause`, `status`, `attempt`, `retries`) + un **evento terminal** (`outcome success|error`, `error_code`, `status`, `final_provider`, `tokens prompt/completion/cache/reasoning`, `cost_usd`, `stream`).
- **Privacidad por construcción:** escribe SOLO el esquema (metadata/agregados). Nunca contenido de prompts/respuestas/tools/headers/keys.
- **Aditivo y reversible:** off por default, no altera el request path. Convive con `/metrics`, `state_file` y el telemetry (009) sin sustituirlos.
- Sobrevive restarts: `O_APPEND|O_CREATE` (permisos `0o640`); sobre archivo no abrible → falla el arranque (fail-fast).
- Un request interrumpido en streaming registra `outcome: success` con los tokens capturados hasta el corte (eventos por request físico; no se pierden eventos terminales).

La **consolidación/visualización** (por día/semana/mes, por provider × modelo × cliente) es un consumidor externo de este JSONL — no toca mofgw.

### `client_config` — fragmentos de config para clientes (016-001)

Configura el endpoint `GET /v1/client-config` que devuelve el **fragmento del provider `mofgw` listo para insertar** en el config de un cliente soportado — para que la config del cliente quede sincronizada con el catálogo real cuando cambian los modelos upstream.

```yaml
client_config:
  base_url: "http://127.0.0.1:3369/v1"   # base pública con la que los clientes llegan a mofgw
  key_env: "MOFGW_KEY"                   # REFERENCIA (nombre de env var); nunca el valor
```

- `base_url` **vacío** (default) → el endpoint responde `503 "client_config.base_url not set"` en runtime (NO fail-fast de arranque; aditivo y off-safe).
- `key_env` es la **referencia** a la variable de entorno de la key del cliente. mofgw **nunca** resuelve ni emite su valor — solo la referencia, en la sintaxis que el cliente usa (`{env:MOFGW_KEY}` en opencode, `${MOFGW_KEY}` en OpenClaw, o sin campo de key en zot, que deriva `MOFGW_API_KEY`).
- Sin `client_config` en el config → la ruta existe pero responde 503/404 de forma segura.

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

Desde 007-002, cada modelo con `model_metadata`/`pricing` en config incluye campos extra que los runtimes leen para decidir modelo y optimizar reasoning:

```json
{
  "id": "model-a",
  "object": "model",
  "context_length": 1048576,
  "context_window": 1048576,
  "max_context_length": 1048576,
  "loaded_context_length": 1048576,
  "max_completion_tokens": 384000,
  "max_output_tokens": 384000,
  "capabilities": { "reasoning": true },
  "thinking": { "levels": ["low", "high", "max"], "default": "low" },
  "supported_parameters": ["tools", "reasoning"],
  "modality": "text+image+video->text",
  "architecture": { "modality": "text+image+video->text", "input_modalities": ["text", "image", "video"], "output_modalities": ["text"] },
  "top_provider": { "context_length": 1048576, "max_completion_tokens": 384000 },
  "pricing": { "input_usd_per_m": 0.14, "output_usd_per_m": 0.28, "cache_hit_usd_per_m": 0.0028 }
}
```

Campos que consumen opencode/openclaw (research §3): `context_length` (openclaw 1º), `top_provider.context_length`/`max_completion_tokens` (openclaw 1º), `max_context_length`/`loaded_context_length` (opencode Go), `max_output_tokens`, `max_completion_tokens`, `capabilities.reasoning`, `thinking.levels/default` (prescriptivo), `supported_parameters` (tools/reasoning), `modality`/`architecture` (visión). Modelos sin metadata conservan el formato mínimo.

### `GET /v1/client-config` (auth, epic 016)

Devuelve el **fragmento del provider `mofgw`** listo para insertar en el config del cliente indicado. Generado desde el catálogo real (misma fuente que `/v1/models`), no hardcodeado: cuando agregás/sacás un modelo o cambia su metadata, el fragmento lo refleja al regenerarlo.

```
GET /v1/client-config?client=opencode
Authorization: Bearer <client-key>
```

| client | Formato devuelto | Campo key |
|---|---|---|
| `opencode` | JSON para la clave `provider` de `opencode.json` (`npm: "@ai-sdk/openai-compatible"`, `options.baseURL`/`options.apiKey`) | `{env:MOFGW_KEY}` |
| `openclaw` | JSON5 para `models.providers` de `~/.openclaw/openclaw.json` (`models[]` array, `api: "openai-completions"`) | `${MOFGW_KEY}` |
| `zot` | JSON para `providers` de `$ZOT_HOME/models.json` (`api: "openai"`) | sin campo de key (zot deriva `MOFGW_API_KEY`) |

La key viaja **siempre como referencia a env var** (o ausente en zot), nunca el valor literal.

**Estados de error (envelope `{"error":{message,type,code}}`):** `401` sin auth válida; `400 invalid_request_error` si falta `client`; `404 not_found_error` si el cliente no está soportado (el mensaje lista los soportados); `503 server_error "client_config.base_url not set"` si `client_config.base_url` no está configurado; `500 server_error` si el renderer falla.

### `GET /v1/usage` (auth)

Consumo acumulado del cliente autenticado (007-003 + 008-003). Cada cliente ve **solo lo suyo**:

```json
{
  "client": "agent-main",
  "totals": { "prompt_tokens": 123, "completion_tokens": 45, "total_tokens": 168, "cache_hit_tokens": 100, "cost_usd": 0.0012 },
  "by_model": [ { "model": "model-a", "prompt_tokens": 123, "completion_tokens": 45, "total_tokens": 168, "cost_usd": 0.0012 } ],
  "sessions": [ { "id": "s1", "requests": 3, "total_tokens": 3840, "cost_usd": 0.003, "last_request_at": 1786 } ]
}
```

Con `?session=<id>` devuelve las stats de **una sesión** del cliente (correlacionada por el header `X-Session-Id` que el runtime manda en los requests):

```bash
curl -sS "http://127.0.0.1:3369/v1/usage?session=abc123" -H "Authorization: Bearer <client-key>"
# → { "session": "abc123", "requests": 3, "prompt_tokens": ..., "total_tokens": ..., ... }
```

- Sesión inexistente → `404`.
- Sin auth → `401`.

### Headers de uso por request (007-003)

Toda respuesta de `/v1/chat/completions` (no-stream y stream) incluye headers con el consumo del request:

```
X-Usage-Prompt-Tokens: 1200
X-Usage-Completion-Tokens: 80
X-Usage-Total-Tokens: 1280
X-Usage-Cache-Hit-Tokens: 1000
X-Usage-Cost-USD: 0.001280
```

En streaming, los headers se setean antes del primer byte (el usage del chunk final va en el body SSE como siempre).

### `X-Session-Id` — correlación de sesiones (008-003)

Header **opcional de solo lectura**: el runtime lo manda para que mofgw agrupe los requests de una sesión lógica y los exponga en `/v1/usage?session=<id>`. **Nunca se reenvía al upstream** (como `X-Agent-Id`).

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
    "model": "model-a",
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
    "model": "model-a",
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
- **Cooldown y accounting en memoria:** al reiniciar mofgw, los providers arrancan "fríos" y el histórico de tokens/costos/sesiones se pierde (no hay persistencia).
- **Estimación de tokens rough:** el rechazo por ventana usa `len(body)/4` (no tokenizer real) — adecuado para prompts gigantes, no exacto.
- **Budget best-effort:** el chequeo de budget es informativo (puede excederse por el costo de un request en vuelo); no es un límite duro.
- **Ventana de budget simple:** acumulado desde el arranque (no deslizante por ventana de tiempo).
- **Sin rate limiting por IP en auth:** aceptado (proxy interno, hash 256-bit; ver TECHDEBT).
- **Métricas en texto plano estilo Prometheus:** el formato Prometheus completo es roadmap.
- **Privacidad de logs:** aun con `--verbose`, **nunca** se loguea contenido de prompts, respuestas LLM, definiciones de tools ni API keys — es por diseño (privacidad). Los logs de debug son solo metadatos (modelo, counts, status, duraciones, causas). Desde SEC-001, los errores de streaming también se sanear antes de llegar al cliente.

## Roadmap (lo que NO está implementado todavía)

- **Métricas Prometheus completas** (005-001, parcial: ya existen contadores en texto plano).
- **Persistencia del accounting** (tokens/costos/sesiones entre restarts).
- **Ventana deslizante exacta** para budget (habilitada por el registro por sesión de 008-003, no aplicada).
- **Alertas** al exceder budget (hoy solo rechazo 429).
- **Budget global** (suma de clientes) — hoy es por cliente.
- **Cache explícito de providers** (cache_control) — el implícito ya da hit rates altos.

---

*Docs del proyecto: [`README.md`](../README.md) · [`docs/epics/plan.md`](epics/plan.md) · specs en [`docs/specs/`](specs/).*
