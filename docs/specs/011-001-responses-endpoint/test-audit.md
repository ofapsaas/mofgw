# Test Audit Report — 011-001-responses-endpoint

**Feature:** `POST /v1/responses` (Responses API de OpenAI para Odoo 19), tronco del epic 011-mofgw-odoo.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec de referencia:** `docs/specs/011-001-responses-endpoint/spec.md` (aprobado por Ofap el 2026-08-12).

## 1. Resumen de comportamiento que cambia

**NO cambia nada existente — endpoint nuevo.** Verificado empíricamente por inspección estática: `grep` por `/v1/responses`, `handleResponses` y `Responses` sobre `internal/` devuelve **cero coincidencias**. El endpoint no existe hoy; no hay ruta, handler ni struct Responses en el código. Todos los tests de esta feature son NUEVOS.

El invariante I1 ("router y providers intactos") confirma que la traducción vive en la capa del endpoint `/responses` y que `provider.ParseChatRequest` + el ruteo de providers no cambian. Por lo tanto ninguna suite existente verifica comportamiento que esta feature modifique.

## 2. Tests modificados

**Ninguno (0).** No existe ningún test existente que valide comportamiento que CAMBIA:
- No hay test que referencie `/v1/responses` (no existe la ruta).
- Los tests que validan el contrato de `/v1/chat/completions` (auth, error envelope, pipeline, cache, usage/costos) se MANTIENEN: el endpoint nuevo reutiliza la MISMA pipeline (P4) y el MISMO envelope (P14/I4) pero NO altera su comportamiento. La ruta nueva se registra "junto a" `/v1/chat/completions` sin reescribirla.
- `ParseChatRequest` no se toca (I1) → `internal/provider/provider_test.go` intacto.

## 3. Tests nuevos a escribir (mapeo a P1–P14, 9 bloques lógicos)

Los tests vivirán en `internal/proxy/e2e_011001_responses_test.go` (nuevo archivo, patrón `proxy_test`, `build()` + `h.srv.URL`, upstreams fake, llamada por HTTP público a `Server.Handler()`). Se agrega un helper `h.responses(t, body)` análogo a `h.chat` (POST a `/v1/responses`).

| Bloque                           | Postcondiciones | Qué verifica (observable)                                                                                                                                                                                                                                                                                                                                                                                 | Refs spec           |
| -------------------------------- | --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------- |
| **A — Auth y ruta**                  | P1              | Sin Bearer → **HTTP 401** con `invalid_api_key`; con Bearer válido → el handler responde (200/400). clientID vía `auth.ClientIDFrom`, sin scoping nuevo. Espeja `TestE2EAuthRequired`.                                                                                                                                                                                                                              | P1, C1              |
| **B — Body inválido**                | P2 (+P14 400)   | `not-json` y body sin `input` usable → **HTTP 400** con envelope `{"error":{type,message,code}}`.                                                                                                                                                                                                                                                                                                                   | P2, C2              |
| **C — Happy path Responses**         | P3, P5, P10     | Body válido `input:[{role:user,content:[{type:input_text,text}]}]` → el upstream recibe `messages:[{role:user,content:"<texto>"}]` (`u.gotBody`); respuesta `choices[0].message.content` → `output[0]` con `{"type":"message","id":"<stable>","role":"assistant","status":"completed","content":[{"type":"output_text","text":...}]}` (parseable por snippet real de Odoo); `model` del envelope == modelo del cliente. | P3, P5, P10, C3, C5 |
| **D — IDs estables**                 | P11             | Mismo request dos veces → mismo `id` de response y de item (determinístico, no aleatorio).                                                                                                                                                                                                                                                                                                                  | P11                 |
| **E — Pipeline compartida**          | P4              | Request que excede context-window → misma respuesta de rechazo que chat; request sobre rate limit → **HTTP 429**; contadores de usage/costo del `clientID` se incrementan.                                                                                                                                                                                                                                      | P4, C4              |
| **F — Rechazos de features futuras** | P6, P7, P8, P9  | Subtests del mismo handler: `stream:true` → 400 "streaming not supported yet"; `tools`/`web_search_preview` → 400 "tool calling not yet supported"; `text.format.json_schema` → 400 "structured output not yet supported"; part `input_file`/`input_image` → 400 "file attachments not yet supported".                                                                                                                | P6–P9, C6–C9        |
| **G — Cache separada por endpoint**  | P12             | Cachear en `/responses` → request equivalente a `/chat/completions` NO se sirve de esa cache (re-ejecuta) y viceversa.                                                                                                                                                                                                                                                                                        | P12, C10            |
| **H — store ignorado**               | P13             | Mismos request con `store` ausente / `false` / `true` → mismo output.                                                                                                                                                                                                                                                                                                                                           | P13, C11            |
| **I — Envelope de error**            | P14             | Shape `{"error":{type,message,code}}` + status correcto en todos: **400** (body inválido / feature no soportada / streaming), **401** (sin auth), **429** (rate limit), **502** (upstream).                                                                                                                                                                                                                                 | P14, C2, C12        |

Nota de diseño (convención CDAD): no se escriben tests por completitud ni coverage; cada test mapea a postcondición. P14 se mantiene como bloque propio para afirmar el envelope uniforme a través de status, aunque partes (400/401) queden cubiertas por B/A — el criterio de aceptación es postcondición verificada, no suma de cobertura.

## 4. Tests untouched (lista EXPLÍCITA)

Toda la suite existente (49 archivos `_test.go`, ~17 paquetes, ~387 tests) queda INTACTA — ninguna feature cambia. Lista explícita por paquete:

**internal/proxy (18 archivos — suite feature-adyacente, la que más cerca está de tocar):**
`e2e_test.go` (incluye `TestE2EAuthRequired`, `TestE2EChatSuccess`, `TestE2EBadRequest`, `TestE2EAllProvidersFail502` — contrato chat intacto, I4), `e2e_003001_test.go` (cache/usage metrics — **cache namespace compartido con P12**), `e2e_006001_test.go` y `e2e_006002_test.go` (usage/cost accounting — **contadores compartidos con P4**), `e2e_008001_test.go` (context-window — **pipeline compartida con P4**), `e2e_limiter_test.go` (rate limit — **compartido con P4**), `e2e_singleflight_test.go`, `e2e_epic009_test.go`, `e2e_007002_test.go`, `e2e_007003_test.go`, `e2e_008002_test.go`, `e2e_008003_test.go`, `e2e_009000_test.go`, `e2e_009001_test.go`, `e2e_009002_test.go`, `e2e_010001_test.go`, `e2e_010002_test.go`, `e2e_010002_requestpath_test.go`.

**internal/auth:** `auth_test.go` (auth sin regresión, I6 — incluye `TestWrapRequiresAuthOnV1`).

**internal/respcache:** `respcache_test.go` (primitiva de cache intacta — P12 usa la misma primitiva).

**Otros paquetes (intactos, sin interacción con la feature):**
- `cmd/mofgw`: `sec001_red_test.go`, `telemetry_red_test.go`
- `internal/clamp`: `clamp_test.go`
- `internal/limiter`: `sec001_red_test.go`, `limiter_test.go`
- `internal/router`: `router_test.go`, `router_sticky_test.go`
- `internal/config`: `context_analysis_test.go`, `telemetry_red_test.go`, `config_test.go`, `sticky_red_test.go`, `external_review_fixes_test.go`, `metadata_red_test.go`
- `internal/timeouts`: `timeouts_test.go`
- `internal/logging`: `telemetry_red_test.go`, `sec001_red_test.go`, `logging_test.go`
- `internal/stream`: `stream_test.go`, `sec001_red_test.go` (streaming es 400 en 001; `RewriteModel` no aplica)
- `internal/singleflight`: `singleflight_test.go`
- `internal/metrics`: `context_test.go`, `metrics_test.go`, `persist_context_test.go`, `persist_test.go`
- `internal/provider`: `provider_test.go` (I1 — `ParseChatRequest` intacto)
- `internal/composition`: `analyze_test.go`, `walk_test.go`
- `internal/health`: `health_test.go`
- `internal/absorb`: `absorb_test.go`

## 5. Regression risk assessment

**Riesgos identificados (sí):**

1. **ALTO — P12 cache namespace compartido (spec lo marca como "VERIFICAR: mecanismo de cache existente").** La primitiva `respcache` se comparte entre endpoints. Si el implementer no separa las claves de cache por endpoint, una chat-response se puede servir como responses-response (o viceversa). Cubierto por el test nuevo G (P12/C10); los tests de cache existentes (`e2e_003001_test.go`) deben seguir verdes. Riesgo acotado pero presente: es la única postcondición donde el spec pide verificación empírica del mecanismo actual.
2. **MEDIO — P4 contadores de usage/costos compartidos.** El endpoint nuevo reutiliza la pipeline de chat. Riesgo de doble-conteo o no-conteo para el `clientID`. Cubierto por el test nuevo E (P4/C4); los tests de accounting (`e2e_006001/006002_test.go`, `e2e_008002_test.go` budget) deben seguir verdes.
3. **MEDIO-BAJO — I6 ruta bajo auth.Wrap.** Riesgo de que la ruta nueva se registre fuera del `Wrap` (pública). Cubierto por el test nuevo A (P1/C1) + tests auth existentes intactos.
4. **BAJO — I3 zero llamadas a OpenAI.** Riesgo de que el handler nuevo hardcodee un destino upstream. Cubierto por el patrón transparencia/no-red de la suite existente + test C.
5. **BAJO — I1 router/providers intactos.** Si el implementer toca `ParseChatRequest` o el ruteo, rompe `provider_test.go` y `router_test.go` (untouched) — señal temprana de drift. Monitorear.

**Beneficio de duda / VERIFICAR (spec §Verificaciones pendientes):**
- Valores exactos de `type`/`code` en envelopes de rechazo (P6–P9) — se fijan en los tests de contrato (bloque F). No bloquea el audit; se resuelve al fijar los asserts en RED.

## 6. Gate de Test Audit checklist

- [x] Audit realizado contra spec aprobado (P1–P14, I1–I6, C1–C12 leídos).
- [x] Cero tests existentes que validen comportamiento que cambia — verificado por `grep` (no existe `/v1/responses`, `handleResponses`, `Responses`).
- [x] Lista de tests untouched EXPLÍCITA (toda la suite, 49 archivos / ~17 paquetes, §4).
- [x] Regression risks identificados y documentados (§5).
- [x] Mapeo de tests nuevos → 14 postcondiciones (9 bloques, §3).
- [x] **VERIFICATION gap resuelto por el orquestador:** `go build ./...` → Success (exit 0); `go vet ./...` → No issues found (exit 0). Baseline compila y está limpio antes de RED.

## Conclusión
La feature es un endpoint nuevo sin impacto sobre la suite existente: **0 tests a modificar, toda la suite untouched (M), 14 postcondiciones mapeadas a 9 bloques de tests nuevos**. Riesgos de regresión acotados y con test de cobertura previsto (P12 cache, P4 counters, I6 auth). Baseline empírico confirmado (`go build` + `go vet` exit 0 por el orquestador).

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12) on 2026-08-12
