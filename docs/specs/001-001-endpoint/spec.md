# Spec — 001-001-endpoint: Servidor HTTP OpenAI-compatible único

---
feature_id: 001-001-endpoint
epic: mofgw-001-core
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: —
paralelizable: sí
---

```yaml
context: >
  mofgw expone UN endpoint OpenAI-compatible a los agentes clientes
  (OpenClaw, crons, hb, agentes clientes). El cliente habla solo con mofgw;
  mofgw habla con los providers upstream (provider-a, provider-b, provider-c,
  provider-d). Esta feature es la piel del proxy: el servidor HTTP que acepta
  requests, las valida, las delega (a un provider único en el MVP de esta
  feature; la cadena de fallback llega en 001-003) y devuelve respuestas
  en el formato exacto que el cliente espera.
resolution: >
  Un agente que hoy apunta a https://api.openai.com/v1 puede apuntar a
  http://localhost:3369/v1 sin cambiar código: misma forma de request,
  misma forma de response (JSON y SSE), mismo formato de error. El reemplazo
  de omniroute arranca acá.
postcondition: >
  Un servidor HTTP en el puerto configurado (default 3369) responde
  POST /v1/chat/completions (no-stream y stream) y GET /v1/models con el
  contrato OpenAI-compatible exacto, usando un provider upstream real o fake,
  y GET /healthz responde 200; todos los tests de la verification pasan.
verification:
  - go test ./... → 0 failures (suite completa, sin red externa)
  - POST /v1/chat/completions no-stream con body válido → 200, JSON con
    choices[0].message.content, id, model (el que pidió el cliente), usage
  - POST /v1/chat/completions con stream:true → 200, Content-Type
    text/event-stream, chunks SSE con data: {"choices":[{"delta":{...}}]}
    y cierre [DONE]
  - GET /v1/models → 200, JSON array con al menos el/los modelos del provider
    configurado
  - GET /healthz → 200 con body "ok" (o JSON {"status":"ok"})
  - POST con body malformado → 400 con error OpenAI-compatible
    ({"error":{"message":...,"type":"invalid_request_error"}})
  - POST a método no soportado (GET /v1/chat/completions) → 405
  - Ruta inexistente → 404 con error OpenAI-compatible
  - El campo model de la response es el que pidió el cliente, no el del
    upstream (reescritura activa)
  - Con el upstream caído (fake que devuelve 500) → 502 con error
    OpenAI-compatible (el fallback a otro provider es 001-003, fuera de acá)
  - max body size: request > 10MB → 413
  - Los tests corren con httptest (fake upstream), cero llamadas reales
```

## Contrato de API

### POST /v1/chat/completions

Request (OpenAI-compatible, subset mínimo validado):

```json
{
  "model": "deepseek-v4-flash",
  "messages": [{"role": "system", "content": "..."},
               {"role": "user", "content": "..."}],
  "stream": false,
  "max_tokens": 2048,
  "temperature": 0.7
}
```

- `model` — **obligatorio**, string no vacío. Si ningún provider sirve el
  modelo pedido (match exacto en el MVP; alias mapping fuera de scope, EPIC-004)
  → 400 `{"error":{"type":"invalid_request_error", ...}}`.
- `messages` — **obligatorio**, array no vacío, cada item con `role` y
  `content` válidos.
- `stream` — opcional, bool, default false.
- Campos ignorados (passthrough al upstream): `temperature`, `max_tokens`,
  `top_p`, `stop`, `presence_penalty`, etc. mofgw no los interpreta en el
  MVP; los reenvía tal cual (clamp de max_tokens es 001-004).
- `max_tokens` — si el cliente pide más de lo que el provider soporta, en el
  MVP el upstream lo rechaza; el clamp real llega en 001-004.

Response no-stream (200):

```json
{
  "id": "chatcmpl-<id upstream o generado>",
  "object": "chat.completion",
  "created": 1720000000,
  "model": "deepseek-v4-flash",
  "choices": [{"index": 0, "message": {"role": "assistant", "content": "..."},
               "finish_reason": "stop"}],
  "usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
}
```

- `model` **siempre** el que pidió el cliente (reescritura), aunque el
  upstream haya usado otro nombre interno.
- `id` del upstream si viene; si no, `chatcmpl-mofgw-<hash>`.

Response stream (200, `Content-Type: text/event-stream`):

```
data: {"id":"chatcmpl-...","object":"chat.completion.chunk","created":...,"model":"...","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"...","object":"chat.completion.chunk","created":...,"model":"...","choices":[{"index":0,"delta":{"content":"hola"},"finish_reason":null}]}

data: {"id":"...","object":"chat.completion.chunk","created":...,"model":"...","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

- Headers SSE: `Content-Type: text/event-stream`, `Cache-Control: no-cache`,
  `Connection: keep-alive` (el ReverseProxy de Go ya lo maneja con
  `FlushInterval` adecuado).
- El primer chunk SSE incluye `delta.role: "assistant"` (compatibilidad
  OpenAI). Si el upstream no lo manda, mofgw lo inyecta.

Error (formato OpenAI-compatible, código HTTP del upstream si es mapeable):

```json
{"error": {"message": "<texto>", "type": "<tipo>", "code": "<code opcional>"}}
```

| Situación | HTTP | type |
|-----------|------|------|
| Body malformado / schema inválido | 400 | `invalid_request_error` |
| model desconocido | 400 | `invalid_request_error` |
| Auth key del cliente inválida (si se implementa) | 401 | `invalid_api_key` |
| Upstream caído (MVP: sin fallback) | 502 | `upstream_error` |
| Timeout de upstream | 504 | `upstream_timeout` |
| Body demasiado grande (>10MB) | 413 | `invalid_request_error` |

### GET /v1/models

```json
{"object": "list", "data": [{"id": "<modelo>", "object": "model",
  "created": 1720000000, "owned_by": "mofgw"}]}
```

- Lista los modelos que el provider configurado expone (o los alias de
  config). En el MVP con 1 provider: sus modelos. Cuando 001-003 agregue la
  cadena, será la unión de modelos de todos los providers.

### GET /healthz

- 200 `ok` — usado por systemd (ExecStartPost) y monitoreo. Sin auth.

## Interfaz Provider (contrato compartido del epic)

```go
type Provider interface {
    ID() string
    Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error)
    MaxTokens() int
}
```

- Definida en `internal/provider/`. 001-001 consume SOLO `Complete` y
  `Stream` de un provider único (el primero del chain de config).
- El cliente OpenAI-compatible por upstream vive en `internal/provider/`
  (habla JSON + SSE, que es lo que hablan todos nuestros upstreams).

## Config mínima consumida por esta feature

```yaml
server:
  addr: "127.0.0.1:3369"        # default; 0.0.0.0 solo si se pide explícito
  max_body_bytes: 10485760      # 10MB
providers:
  - id: provider-a
    base_url: "https://api.example.com/v1"
    api_key_env: "MOFGW_PROVIDER_A_KEY"   # key NUNCA en el YAML; env var
    models: ["deepseek-v4-flash", "glm-5.2"]
```

- La carga/validación completa de config es 001-002; esta feature usa un
  subset mínimo (server + primer provider). El archivo default es
  `~/.config/mofgw/config.yaml` (ver 001-002 para la resolución home/sistema).

## Seguridad (mínima del MVP)

- API keys de upstream solo vía env vars referenciadas en config; nunca en
  el YAML, nunca en logs.
- `slog` JSON: los bodies de request/response NUNCA se loguean (pueden
  contener datos de clientes). Se loguean IDs, modelos, códigos de estado,
  duraciones.
- Servidor escucha en `127.0.0.1` por default (los agentes clientes corren
  en la misma máquina). Exponer a red = decisión explícita + auth (fuera de
  scope del MVP).

## No-funcionales

- Timeout TTFB del upstream: 60s default (configurable, 001-006).
- Concurrencia: `http.Server` estándar; un `http.Transport` compartido con
  pooling (`MaxIdleConnsPerHost ≥ 8`, `IdleConnTimeout: 90s`,
  `ForceAttemptHTTP2: true`).
- Semáforo in-flight por provider (8-16) protegiendo providers débiles
  (patrón de research-architecture §1).

## Fuera de scope (features hermanas)

- Cadena de fallback + cooldown → 001-003
- Clamp de max_tokens → 001-004
- Streaming con retry pre-primer-byte → 001-005 (esta feature solo hace
  passthrough SSE del primer provider)
- Timeouts configurables por intento → 001-006
- Config file completa (providers múltiples, orden, validación) → 001-002
- Metrics Prometheus → EPIC-005

## Notas de implementación (para el implementer)

- `cmd/mofgw/main.go` — entrypoint: flags (`-config`), carga config, bootstrap
  del server.
- `internal/proxy/` — wiring `httputil.ReverseProxy` con reescritura de
  `model` en request y response; SSE passthrough con `FlushInterval`.
- `internal/provider/` — interfaz + cliente OpenAI-compatible (stdlib,
  sin deps externas, como el harness de kernel-ia F12).
- Tests herméticos con `httptest.Server` como upstream fake (patrón ya
  validado en kernel-ia: fake LLM server, cero red real).
- El E2E del epic (provider A caído → responde B) es del epic, no de esta
  feature; acá basta con el fake que responde 500 → 502 limpio.

## Estado

- [ ] Spec aprobada (requiere aprobación del plan de epics, P1)
- [ ] Implementación MVP
- [ ] Tests verdes + E2E manual con un provider real
