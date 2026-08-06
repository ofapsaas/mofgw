# Spec — 005-005-verbose: Flags --verbose y --log-file (debuguear el proxy)

---
feature_id: 005-005-verbose
epic: mofgw-005-ops
status: draft (aprobado por Pablo en sesión, 05 Ago 2026)
created_at: 2026-08-05
updated_at: 2026-08-05
depends_on: 005-002-logging (parcial), 001-001-endpoint, 001-003-fallback
paralelizable: no
---

## Contexto

mofgw es un proxy Go OpenAI-compatible con fallback transparente. Hoy
loguea JSON a stdout con nivel fijo info (`logging.New(slog.LevelInfo)` en
`cmd/mofgw/main.go`). Para poder debuguear en producción (systemd) hace
falta:

1. Flag `--verbose` (bool): activa nivel DEBUG (sin él, nivel INFO).
2. Flag `--log-file <path>` (string): destino configurable de logs a
   archivo (append).

**Niveles (regla Pablo):** sin verbose = unos pocos info ("está vivo" +
cualquier error). Con verbose = detalles para debug de qué recibe, qué
envía y qué decisiones toma el proxy y por qué.

**PRIVACIDAD (regla dura):** NUNCA loguear en este feature: contenido de
prompts de usuario (`messages[].content`), contenido de respuestas LLM
(`choices`/delta content), definiciones de tools, ni API keys. Solo
METADATOS (counts, ids, modelos, status, duraciones, causas). El contenido
de prompts/respuestas se diseñará en un feature futuro aparte con medidas
de seguridad/privacidad — NO ahora.

**Topología de destinos (regla 3 confirmada):**

| Nivel | Sin --log-file | Con --log-file |
|-------|---------------|----------------|
| info (default) | stdout | stdout + archivo |
| verbose (debug) | stdout | SOLO archivo (stdout queda limpio con info/errores) |

Es decir: `--log-file` NO cambia el destino de info (siempre stdout, y
además archivo). Lo que cambia es verbose: sin archivo → stdout; con
archivo → solo archivo.

## Postcondición

mofgw acepta `--verbose` (bool → nivel debug) y `--log-file <path>`
(destino archivo, append). Sin verbose → nivel info. Destinos según la
tabla de Contexto. Ningún log (a ningún nivel ni destino) incluye
contenido de prompts/respuestas/tools/keys. Todos los tests de la
verification pasan.

## Cambios de código esperados (no vinculantes, orientación)

- `cmd/mofgw/main.go`: registrar flags `--verbose` y `--log-file`,
  construir el logger con nivel y destinos correctos (`io.MultiWriter` o
  handler fanout), fail-fast si el archivo no se puede abrir.
- `internal/logging/logging.go`: soportar múltiples destinos con niveles
  distintos (ej. `New` con opciones, o una función `Build(level,
  logFile)` que devuelva el logger + closer). Mantener compatibilidad con
  la API actual si los tests existentes la usan.
- `internal/proxy/proxy.go`: eventos `request_start` / `request_end`
  (debug).
- `internal/router/router.go`: eventos `provider_attempt` /
  `provider_fallback` / `cooldown_*` / `decision` (debug).

## Eventos a instrumentar

Todos DEBUG salvo los que hoy ya son info/warn/error. Hoy ya existen
(info/warn): config cargado, mofgw escuchando, provider en cooldown
(warn), chain error (warn), intento fallido. Eventos debug a agregar:

1. **request_start** (proxy, al recibir POST /v1/chat/completions):
   method, path, stream (bool), model pedido, num_messages (count),
   num_tools (count). SIN contenido.
2. **provider_attempt** (router, por cada intento): provider, attempt
   (i/N), timeout_aplicado, TTFB ms al responder.
3. **provider_fallback** (router, cuando un provider falla retryable):
   provider_fallido, causa_clasificada (429/5xx/timeout/red),
   proximo_provider.
4. **cooldown_start/cooldown_end** (router): provider, duración.
5. **request_end** (proxy, al completar): status_code, duración_total ms,
   provider_servidor (si hubo), model pedido.
6. **decision** (router): por qué se eligió/saltó un provider (ej:
   "provider en cooldown", "no sirve modelo", "fallo retryable").

## Reglas de privacidad (testeables)

- Ningún log emitido por el proxy (a cualquier nivel) contiene el texto de
  un prompt enviado o de una respuesta recibida.
- Ningún log contiene definiciones de tools ni API keys.

## Verification

- `go test ./... -race` → 0 failures
- Test: sin verbose, un request → solo info/warn/error a stdout (sin
  eventos debug)
- Test: `--verbose` sin archivo → debug a stdout
- Test: `--verbose` con archivo → info a stdout+archivo, debug SOLO
  archivo
- Test: `--log-file` sin verbose → info a stdout+archivo, sin debug
- Test negativo: request con prompt "S3CRETO-PROMPT-123" → ese string NO
  aparece en ningún log (ni stdout ni archivo, ni info ni debug)
- Test: archivo de log no abrible → error claro al arranque (fail-fast)

## Contratos con otras features

- **005-002-logging:** este feature consume y extiende el logger JSON de
  005-002 (mismo formato, mismos eventos canónicos `request_start` /
  `provider_attempt` / `provider_fallback` / `cooldown_*` /
  `request_end`). 005-002 queda como formato base; 005-005 agrega nivel y
  topología de destinos. Los tests existentes de 005-002 deben seguir
  pasando sin cambios de API.
- **001-001-endpoint:** los eventos `request_start` / `request_end` se
  emiten desde el handler de `/v1/chat/completions` que define 001-001.
- **001-003-fallback:** `provider_fallback` y `cooldown_*` se emiten
  desde los mismos puntos donde 001-003 decide, reusando su clasificación
  de causa retryable (429/5xx/timeout/red).
- **005-003-systemd:** con `--log-file`, el deploy systemd puede apuntar
  el log a un archivo dedicado sin contaminar journald; sin archivo, el
  comportamiento actual (journald captura stdout) se mantiene intacto.

## Riesgos y mitigaciones

- **Cambio de API de logging rompe tests de 005-002:** mitigación —
  mantener `New(level slog.Level)` como firma existente y agregar la
  variante nueva como opción/función adicional (Build). Los tests de
  005-002 no se tocan.
- **Hot path (streaming post-primer-byte):** los eventos debug se emiten
  por request, no por token; ninguna fase post-primer-byte loguea
  contenido (regla dura de privacidad, misma mitigación que 005-002).
- **Archivo de log llenándose / rotación:** append simple sin rotación en
  esta feature; la rotación y retention se dejan fuera de alcance
  (depende del operador/systemd o feature futura).

## Fuera de alcance

- Logging de contenido de prompts/respuestas (feature futuro con
  privacidad)
- Retry/backoff (002-001), health checks (002-002) — otros epics
- Rotación y retention del archivo de log
