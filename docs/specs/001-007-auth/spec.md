# Spec — 001-007-auth: API key por cliente (Bearer), 401, hash en disco

---
feature_id: 001-007-auth
epic: mofgw-001-core
status: draft
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-002-config
paralelizable: sí
---

```yaml
context: >
  mofgw va a ser el único punto de entrada de los agentes clientes
  (OpenClaw, crons, sesiones aisladas, agentes privados). En el deploy por usuario,
  las keys de providers viven SOLO en mofgw, nunca en los clientes.
  Pero eso significa que cualquiera que llegue al puerto 3369 puede usar
  los providers pagos de la cuenta. Esta feature protege el endpoint:
  cada cliente tiene su propia API key (Bearer token), las keys se
  verifican contra un hash SHA-256 en disco, y los endpoints de
  operación (/healthz, /metrics) quedan sin auth para monitoreo local.
resolution: >
  mofgw acepta requests a /v1/* solo con header `Authorization: Bearer
  <key>` donde <key> coincide (hash SHA-256) con una entrada de
  clients[] del config. Si falta el header, la key es inválida o el
  hash no coincide → 401 con body OpenAI-compatible
  {"error":{"message":"Invalid API key","type":"invalid_api_key",...}}.
  Las keys NUNCA se guardan en claro en disco: el config referencia un
  archivo de clients (o campo clients) con `key_sha256: <hex>`; el
  agente/operador genera el hash con `mofgw hash-key` (subcomando CLI).
  /healthz y /metrics responden 200 sin auth (loopback; bind default
  127.0.0.1). El timing de la comparación no depende de la longitud de
  la key (hash compare constante).
postcondition: >
  Todo request a /v1/chat/completions y /v1/models sin Bearer válido
  recibe 401 invalid_api_key y NO toca ningún provider upstream.
  Con Bearer válido el request pasa. /healthz y /metrics funcionan sin
  auth. Las keys en disco son hashes SHA-256, nunca texto plano.
verification:
  - go test ./internal/auth/ → 0 failures (sin red externa)
  - Request sin header Authorization → 401 con tipo invalid_api_key
  - Request con Bearer key desconocida → 401 invalid_api_key
  - Request con Bearer key válida (hash en clients) → pasa al handler
    (200 del upstream fake en test E2E)
  - clients[] con key_sha256 correcto → valida; con hash malformado
    (no hex / longitud ≠ 64) → error de validación al cargar config
  - /healthz y /metrics → 200 sin header de auth
  - `mofgw hash-key` imprime sha256 hex de la key (stdin o arg), sin
    loguear la key en claro
---
```
