# Spec — Hotfix: ParseChatRequest debe aceptar content como array (transparencia)

---
feature_id: 001-001-endpoint-hotfix-content-array
epic: mofgw-001-core (hotfix)
status: draft (aprobado por Pablo en sesión, 05 Ago 2026)
created_at: 2026-08-05
updated_at: 2026-08-05
depends_on: 001-001-endpoint
paralelizable: no
---

## Contexto

Bug verificado en producción (05 Ago 2026): OpenClaw recibe un 400 de mofgw al
enviar un request legítimo a `/v1/chat/completions`. Error en el journal:

```
provider=mofgw model=deepseek-v4-flash status=400
rawError=400 invalid request body: json: cannot unmarshal array into Go struct field ChatMessage.messages.content of type string
→ FailoverError: provider rejected the request schema or tool payload
```

Causa raíz en `internal/provider/provider.go`: `ParseChatRequest` hace un
`json.Unmarshal` estricto del body contra la vista de ruteo, donde cada mensaje
declara `Content string`:

```go
type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Stream    bool          `json:"stream,omitempty"`
	MaxTokens *int64        `json:"max_tokens,omitempty"`
}
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"` // ← rompe cuando content es un ARRAY
}
```

OpenClaw envía `messages[].content` como array de partes
(`[{"type":"reasoning",...},{"type":"text",...}]` — formato típico de modelos
reasoning como deepseek-v4-flash). Go no puede unmarshal un array en un string
→ mofgw rechaza con 400 un request que el provider upstream aceptaría.

Principio violado: el proxy es transparente — el body viaja crudo ([]byte) y
la vista de ruteo sirve SOLO para decidir (model/stream/max_tokens + count de
messages). El parseo de ruteo NUNCA debe rechazar un request válido de OpenAI
por la forma del contenido de messages. mofgw no usa `Content` en ningún
punto del camino de ruteo: solo cuenta `len(req.Messages)` (validación y
`num_messages`).

Hallazgo secundario: en streaming, `request_end` loguea `"provider":""`. En
`internal/proxy/proxy.go`, `handleChat` declara `providerID` y lo setea solo en
el camino no-stream (`providerID = res.ProviderID`); `handleStream` usa
`res.Provider.ID()` internamente (métricas y warn) pero nunca propaga el
provider que sirvió al defer de `request_end`.

## Postcondición

`ParseChatRequest` acepta un body donde `messages[].content` es un string O un
array (o null) sin error. El parseo de ruteo extrae model/stream/max_tokens y
cuenta messages sin interpretar su contenido. Ningún request válido de OpenAI
se rechaza por la forma de content. En streaming, `request_end` reporta el
provider que sirvió (no vacío). Todos los tests pasan.

## Cambios esperados (orientación)

- `internal/provider/provider.go`: `ChatRequest.Messages` pasa a
  `[]json.RawMessage` (cada mensaje queda opaco al parseo de ruteo;
  `len(req.Messages)` sigue funcionando igual). `ChatMessage` (con `Content
  string`) queda SOLO para la respuesta (`ChatResponse.Choices[].Message`) —
  los tests existentes de respuesta lo usan y no cambian.
- `internal/router/router_test.go` (impacto de test): el helper `reqFor`
  construye `ChatRequest` con `[]provider.ChatMessage`; al cambiar el tipo de
  `Messages` debe actualizarse a `[]json.RawMessage` (es test de ruteo, no de
  contenido — sin cambio semántico).
- `internal/proxy/proxy.go` (opcional, hallazgo secundario): en el camino
  streaming, setear el provider en `request_end` (desde
  `res.Provider.ID()` de `StreamResult`, la misma fuente que ya usa
  `SetLastProvider`).

## Verification

- `go test ./... -race` → 0 failures
- Test RED: `ParseChatRequest` con content como array de partes → sin error,
  `num_messages` correcto
- Test existente: content string sigue funcionando (sin regresión)
- Tests existentes de respuesta (`.Content` string en `ChatResponse`) siguen
  pasando
- curl a `/v1/chat/completions` con `messages[].content` = array → 200 (no 400)

## Fuera de alcance

- Logging de contenido de prompts (feature futuro con privacidad)
- Cualquier otro campo del request que OpenClaw pueda enviar en formas no
  estándar (se reporta si aparece)
