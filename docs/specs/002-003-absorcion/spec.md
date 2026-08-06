# Spec — 002-003-absorcion: Absorción de errores — el cliente nunca ve fallo de provider

---
feature_id: 002-003-absorcion
epic: mofgw-002-resiliencia
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 002-001-retry, 001-003-fallback, 001-001-endpoint
paralelizable: no
---

```yaml
context: >
  El principio rector de mofgw es la transparencia total: el cliente nunca
  se entera de que un provider falló. 001-003 absorbe fallos probando el
  siguiente provider de la cadena; 002-001 absorbe fallos transitorios
  reintentando el mismo. Pero hay un caso que ninguna de las dos cubre:
  cuando TODA la cadena falló (todos los providers caídos, en cooldown,
  o con errores no-retryables). En ese momento el proxy DEBE responder
  algo — y ese algo no puede ser: (1) el error crudo del provider (puede
  contener URLs internas, headers de auth, stack traces, detalles de
  implementación), (2) un crash, (3) un hang hasta que el cliente haga
  timeout por su cuenta. Esta feature es la última capa de absorción:
  garantiza que CUALQUIER situación de fallo total produzca una respuesta
  de error coherente, en formato OpenAI-compatible, saneada, con el
  status HTTP correcto, y que el operador pueda diagnosticar desde los
  logs (no desde el mensaje que ve el cliente). También define qué pasa
  con los 4xx de provider: un 400/404 de provider es un bug de config
  del proxy, NO un error del cliente — no debe propagarse como si el
  cliente hubiera mandado algo mal.
resolution: >
  Un paquete puro `internal/absorb` centraliza TODO el mapeo de errores
  salientes. Expone dos funciones:
  1. `Sanitize(err error) *AbsorbedError` — convierte el error de un
     provider en la estructura pública mínima: { StatusCode int, Code
     string, Message string }. Reglas: (a) el mensaje NUNCA contiene
     URLs, headers, keys, IPs, stack traces ni nombres de archivo
     internos — se reemplaza por un mensaje genérico por categoría;
     (b) el detalle completo (con toda la información del error
     original) va SIEMPRE al log del proxy (stderr / log file) con
     el provider ID y timestamp; (c) 4xx de cliente (400, 404, 422)
     originados en el REQUEST del cliente pasan con su status original
     (el router ya los devuelve temprano — regla 001-003 — esta capa
     solo los formatea); (d) 401/403 del provider se mapean a 502 con
     code `provider_auth_error` — es un problema de config del proxy,
     no del cliente, y debe ser visible para el operador en logs; (e)
     errores de red/timeout/5xx se mapean a 502 con code
     `upstream_unavailable`; (f) errores internos del propio proxy
     (panic recuperado, bug) → 500 con code `internal_error`.
  2. `Respond(w, err)` — escribe el body JSON OpenAI-compatible
     `{"error":{"message":...,"type":"...","code":...}}` con el status
     HTTP mapeado y headers estándar (Content-Type, X-Request-ID si
     existe). Es la ÚNICA ruta por la que un error sale del proxy:
     ningún handler escribe un error de provider directo a la
     respuesta.
  El router de 001-003, cuando agota la cadena, llama
  `absorb.Respond(w, lastErr)` en lugar de devolver el error crudo.
  Un `recover()` en el middleware del server (001-001) convierte
  cualquier panic en `absorb.Respond(500)`: el proxy nunca crashea
  por un request. La degradación "todos caídos" con respuestas
  coherentes y estado visible es responsabilidad de 002-004, que
  consume el mismo AbsorbedError (el contrato queda fijado acá).
postcondition: >
  Toda respuesta de error del proxy pasa por internal/absorb: formato
  OpenAI-compatible, status HTTP correcto, mensaje saneado sin detalles
  internos, detalle completo en logs con provider ID. Un 4xx del
  request del cliente se devuelve como tal (no se confunde con fallo
  de provider). Un 401/403 de provider se ve como 502 provider_auth_error
  en el cliente y como error de config en logs. Un panic en cualquier
  handler se convierte en 500 internal_error, no en crash del proceso.
  El proceso nunca cuelga: toda ruta de error termina en una respuesta.
verification:
  - go test ./internal/absorb/ → 0 failures (paquete puro, sin red)
  - test unit: error con URL interna + header de auth → Message
    saneado no contiene la URL ni el header; log contiene ambos
  - test unit: 401 de provider → StatusCode 502, Code
    provider_auth_error, Message genérico
  - test unit: 400 del request cliente → StatusCode 400 (pasa como
    está, sin re-mapear a 502)
  - test e2e (httptest): los 5 providers caídos → respuesta 502 con
    body OpenAI-compatible válido en < 2s (sin crash, sin hang)
  - test e2e: provider que devuelve 500 → cliente recibe 502
    saneado; log del proxy contiene el error original con provider ID
  - test panic: handler que panic()ea → respuesta 500 internal_error,
    proceso sigue vivo (siguiente request funciona)
contracts:
  AbsorbedError: >
    struct público de internal/absorb: { StatusCode int, Code string,
    Message string, Type string (p.ej. "upstream_error"), ProviderID
    string, Err error (original, solo para logs) }.
  Mapeo de status: >
    4xx cliente → mismo status (formato OpenAI); 401/403 provider →
    502 provider_auth_error; red/timeout/5xx/429 agotado → 502
    upstream_unavailable; panic/bug interno → 500 internal_error;
    todo lo demás → 502 upstream_unavailable.
  Logging: >
    El log del error original SIEMPRE incluye: provider_id, intento
    (n/Total), error original completo, request_id. Formato: línea
    JSON a stderr (o log file si config 001-002 lo define), nivel
    error. Los logs NUNCA se envían al cliente.
  Dependencia con 002-004: >
    002-004-degradacion consume AbsorbedError y el estado del router
    para decidir respuestas cuando todos los providers están caídos;
    el formato del error al cliente queda definido acá (002-004 solo
    agrega estado visible y política de degradación).
out_of_scope:
  - Retry adicional en esta capa (ya vive en 002-001)
  - Persistencia de errores (colas, dead-letter) — post-MVP
  - Dashboard de errores (EPIC-005 metrics)
  - Traducción/localización de mensajes
```
