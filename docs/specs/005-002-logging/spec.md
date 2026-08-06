# Spec — 005-002-logging: Logging estructurado JSON

---
feature_id: 005-002-logging
epic: mofgw-005-ops
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: —
paralelizable: sí
---

```yaml
context: >
  Sin logs estructurados, operar mofgw en producción es adivinar: los
  logs de texto plano no se parsean, no se correlacionan (¿este fallback
  fue del mismo request que ese timeout?), y no se pueden ingestar en
  un agregador (Loki, ELK, journald --output=json). Los agentes y el
  operador necesitan: saber qué pasó con un request concreto (trazar por
  request_id), cuándo un provider entró/salió de cooldown (001-003), y
  distinguir niveles (debug/info/warn/error) sin ambigüedad. Esta
  feature define UN formato (JSON en una línea por evento), UN logger
  (slog, stdlib), y campos obligatorios por tipo de evento para que todo
  el sistema hable el mismo idioma. La consistencia es el requisito: un
  request_id que aparece en 5 líneas debe poder filtrarse con un grep.
resolution: >
  internal/logging envuelve log/slog con configuración mínima: salida
  JSON a stdout (stderr reservado para fatal de arranque), nivel por
  config (default info; debug activable con --log-level o config.log).
  Campos globales en cada evento: ts (RFC3339 con ms), level, logger
  (constante "mofgw"), request_id (uuid propagado desde el handler si
  aplica), provider (id del provider si aplica). Eventos canónicos:
  1. startup (config loaded, providers N, listening :3369);
  2. request_start (method, path, stream bool, model pedido);
  3. provider_attempt (provider, intento i/N, TTFB ms);
  4. provider_fallback (provider fallido, causa, próximo);
  5. cooldown_start / cooldown_end (provider, duración);
  6. request_end (status_code, duración total ms, tokens si los expone
     upstream, err final si hubo);
  7. health_check (provider, ok bool, latencia ms) — debug level;
  8. degraded_mode (entrada/salida, providers_down N) — warn;
  9. error_event (tipo, mensaje saneado — sin keys, URLs internas ni
     bodies crudos; consistente con el saneamiento de 001-003).
  Errores de config al arranque: stderr con el error YAML descriptivo
  (misma regla fail-fast de 001-002) y exit code 1. NUNCA se loguean
  API keys ni tokens completos (solo últimos 4 chars si es necesario
  para debugging).
postcondition: >
  Todo evento de mofgw sale como JSON de una línea en stdout,
  parseable y filtrable por request_id. Un request con fallback se
  puede trazar de punta a punta (request_start → attempt 1 → fallback
  → attempt 2 → request_end) sin ambigüedad. Sin keys ni datos
  sensibles en los logs.
verifications: >
  - `go vet ./...` y `go test ./internal/logging/` → 0 failures.
  - Logger emite JSON válido: cada línea parsea con encoding/json
    (test con 100 eventos mixtos).
  - request_id se propaga del handler al router al provider (test de
    integración: stream con fallback → todas las líneas del request
    comparten request_id).
  - Nivel debug oculta los eventos debug por defecto; --log-level=debug
    los muestra.
  - Un error de config al arranque → stderr + exit 1, sin JSON.
  - Test negativo: mensaje de error con URL interna → saneado antes de
    loguear (assert: la URL no aparece en la línea).
```

## Contratos con otras features

- **001-002-config:** nivel de log viene de `config.log.level` (default `info`), flag `--log-level` lo sobreescribe.
- **001-003-fallback:** los eventos `provider_fallback` y `cooldown_*` se emiten desde los mismos puntos donde 001-003 decide, usando el logger inyectado (interfaz `MetricsSink` no-op de 001-003 se extiende a logging opcional, nunca bloqueante).
- **005-001-metrics:** logging y metrics comparten el `request_id` y los mismos eventos (log ≠ métrica: el log tiene contexto, la métrica tiene contador).
- **005-003-systemd:** journald captura stdout → los logs JSON llegan a journald sin configuración extra; `journalctl -u mofgw -o json` funciona.

## Riesgos y mitigaciones

- **Overhead de logging en hot path:** slog con handler JSON es ~µs por evento; el hot path de streaming (post-primer-byte, 001-005) NO loguea por token — solo por evento de request/error. Mitigación explícita: fase post-primer-byte emite únicamente `request_end` y errores.
- **Saneamiento incompleto:** la lista de patrones a sanear es centralizada en internal/logging (función `sanitize()`), testeada con los mismos casos de 001-003.
- **Logs duplicados (stdout + stderr):** decisión: stdout = todo lo estructurado; stderr = solo fatal de arranque pre-logger (no hay logger configurado todavía).
