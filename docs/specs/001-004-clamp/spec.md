# Spec — 001-004-clamp: Clamp de max_tokens por provider

---
feature_id: 001-004-clamp
epic: mofgw-001-core
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-002-config
paralelizable: sí
---

```yaml
context: >
  Cada provider tiene un límite de max_tokens distinto (provider-c
  rechaza con 400 si el request pide más que su máximo; otros providers
  lo ignoran silenciosamente o fallan con errores oscuros). El cliente
  del proxy (OpenClaw, agentes clientes, crons) pide con max_tokens altos sin
  saber los límites de cada upstream. El proxy debe ajustar (clamp) el
  max_tokens del request al máximo del provider al que va a hablar, de
  forma transparente: el cliente nunca se entera de que su valor fue
  reducido, y el upstream nunca recibe un valor que rechace.
resolution: >
  Al enrutar un request hacia un provider concreto, el proxy compara el
  max_tokens pedido por el cliente contra el límite configurado del
  provider (ProviderConfig.MaxTokens, default 0 = sin límite). Si el
  pedido excede el límite, se reescribe el campo max_tokens del body al
  valor límite antes de reenviar. El response nunca se modifica: el
  cliente recibe el response del upstream tal cual (incluyendo el
  usage.completion_tokens real). El clamp es por-request, no muta config.
postcondition: >
  Ningún request reenviado a un provider supera su max_tokens configurado.
  Un request con max_tokens=0 o ausente NO se toca (se deja como está).
  Un provider con max_tokens=0 (sin límite configurado) NO clampea.
  El clamp aplica también al campo max_completion_tokens si el request
  lo trae (API nueva estilo OpenAI); si vienen ambos, se clampea el que
  exceda y se deja el otro sin tocar (no se inventan valores).
verification:
  - go test ./internal/clamp/ → 0 failures (sin red externa)
  - Request max_tokens=131072 + provider max_tokens=16384 → body
    reescrito a 16384 antes del upstream; upstream fake recibe 16384
  - Request max_tokens=4096 + provider max_tokens=16384 → body intacto
    (no se toca)
  - Request sin max_tokens + provider con límite → body intacto
  - Provider max_tokens=0 (sin límite) + request 131072 → body intacto
  - max_completion_tokens=99999 + provider 16384 → clampeado a 16384
  - Response del upstream se devuelve al cliente sin reescrituras
    (fixture: usage.completion_tokens > clamp límite se preserva)
```

## Contrato de API

La feature NO expone endpoints nuevos. Es una transformación del body del
request en el path de reenvío (001-001) y en el path de fallback (001-003).

```go
// internal/clamp/clamp.go — paquete puro, sin I/O
// ClampBody reescribe max_tokens/max_completion_tokens del body JSON
// del request según el límite del provider. Devuelve el body ya
// clampeado (nuevo slice) y un bool indicando si hubo cambio.
func ClampBody(body []byte, providerMaxTokens int) ([]byte, bool, error)

// Reglas:
//  - providerMaxTokens <= 0 → sin clamp (body intacto, changed=false)
//  - max_tokens ausente o 0 en el body → no se toca ese campo
//  - max_tokens > providerMaxTokens → reescribir a providerMaxTokens
//  - max_completion_tokens idéntico tratamiento (campo independiente)
//  - JSON malformado → error (no se propaga un body roto al upstream)
```

### Integración
- `internal/proxy/` (001-001): antes de `ReverseProxy.ServeHTTP`, decodificar
  el body (respetando `ServerConfig.MaxBodyBytes`), clampear, re-empaquetar.
- `internal/router/` (001-003): al elegir provider B tras fallo de A,
  re-clampear con el límite de B (A y B pueden tener límites distintos).
- El clamp corre SIEMPRE con el límite del provider destino, en el momento
  del reenvío (no cachear el body clampeado entre intentos).

## Interfaz consumida (de 001-002)

```go
// internal/config — campos relevantes para esta feature
type ProviderConfig struct {
    ID        string `yaml:"id"`
    MaxTokens int    `yaml:"max_tokens"` // 0 = sin límite (default)
    // ... resto de campos ver spec 001-002
}
```

## Seguridad
- El clamp NUNCA loguea el body completo (puede contener prompts con datos
  del cliente). Solo loguear `{provider, max_tokens_before, max_tokens_after}`
  en debug.
- No se reescriben otros campos del request: solo max_tokens y
  max_completion_tokens. Cualquier otra transformación es de otra feature.

## No-funcionales
- Coste: una decode+encode JSON por request reenviado. Aceptable para el
  MVP (los bodies de chat completions son pequeños); si el profiling lo
  pide, optimizar con reescritura de campo en raw JSON (ver research).
- Determinismo: mismo input → mismo output. Sin estado, sin rand.

## Fuera de scope (features hermanas)
- Fallback en cadena + cooldown → 001-003
- Timeouts por intento → 001-006
- Streaming con retry pre-primer-byte → 001-005 (el clamp aplica al body
  inicial del request, idéntico en streaming y no-streaming)
- Validación del YAML / defaults → 001-002

## Notas de implementación (para el implementer)
- Paquete puro `internal/clamp/` con tests de tabla: cero red, cero deps
  externas (solo `encoding/json`), patrón ya validado en kernel-ia.
- Test del flujo completo: `httptest.Server` fake que registra el body que
  recibe; assert del `max_tokens` recibido.
- El límite del provider se lee de la Config ya cargada (001-002); esta
  feature NO vuelve a parsear YAML.
- E2E del epic (qwen con 131072 → clamp a límite configurado) es del epic,
  no de esta feature; en unit tests basta el fake.
- Decisión de diseño: clampear a `provider.max_tokens` tal cual viene del
  YAML. Si un provider documenta límite menor que el configurado, es un
  error de configuración (se detecta en validación 001-002, no en clamp).

## Estado
- [ ] Spec aprobada (requiere aprobación del plan de epics, P1)
- [ ] Implementación MVP
- [ ] Tests verdes + E2E manual con un provider real
