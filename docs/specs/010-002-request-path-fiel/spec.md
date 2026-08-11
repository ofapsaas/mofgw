# Spec — 010-002-request-path-fiel: el request path respeta el catálogo (clamp por modelo, window-check fiel, inyección del default prescriptivo)

---
feature_id: 010-002-request-path-fiel
epic: mofgw-010-catalogo-fiel
status: approved
approved_by: Ofap (usuario delegado HITL — auto-aprobación GVR, delegación Pablo)
approved_at: 2026-08-11
created_at: 2026-08-11
depends_on: 010-001 (ModelMetadata — max_output/thinking_default), 008-001 (rechazo por ventana — el window check se modifica), 001-004 (clamp por provider — se conserva), 001-003 (routing/fallback — sin cambios)
paralelizable: no
---

## Contexto

El catálogo (010-001, aprobado) ya declara `max_output` y `thinking_default`
prescriptivos por modelo, pero el **request path los ignora**. Tres huecos de
fidelidad verificados en código y operación:

1. **max_tokens fuera del techo real del modelo → 400→502.** kimi-k2.7-code
   tiene `max_output` 32768; un cliente que manda `max_tokens: 384000`
   (defaults de runtimes) hace que el upstream responda 400 (max_tokens >
   max_output) → `classify()` lo trata como no-reintentable → la cadena se
   corta → el cliente ve 502. Mismo mecanismo documentado en TECHDEBT #23 para
   la ventana (prompt+completion contra el límite real), ahora por el techo de
   output.
2. **Window-check estima la completion con el max_tokens CRUDO (008-001 +
   TECHDEBT #23).** El chequeo (proxy.go `exceedsContextWindow`, proxy.go:370)
   suma `promptTokens + maxTokensCrudo` contra `context_window × (1+margin)`.
   Cuando `max_output < max_tokens` del cliente, la completion REAL no puede
   superar `max_output` → el chequeo sobre-estima y rechaza requests que en
   realidad caben (falso positivo). El fix TECHDEBT #23 (contar la completion
   contra la ventana real) NO se reabre: la ventana se chequea con el valor
   EFECTIVO post-clamp por modelo, que es ≤ crudo — el upstream sigue siendo
   la autoridad final.
3. **Default prescriptivo declarado pero no inyectado (ADR-003).** La metadata
   declara p. ej. deepseek-v4-flash `thinking_default: low`; el nativo del
   modelo es high. Un cliente sin effort explícito corre al nativo (más costo/
   latencia) — el gateway no materializa lo que el catálogo prescribe.

### Hallazgos verificados (código + research, acceso 2026-08-10/11)

- **Clamp por provider hoy (001-004):** `clamp.Request(body, providerMax)`
  (internal/clamp/clamp.go) reescribe `max_tokens`/`max_completion_tokens` si
  superan el límite del provider; el router lo aplica POR INTENTO
  (`clampBody`, router.go:599 y 720). El router NO conoce la metadata del
  modelo (sin referencia a modelMeta).
- **Metadata en el proxy (007-001):** `proxy.Server.modelMeta`
  (`map[string]config.ModelMetadata`, setter `SetModelMetadata`, proxy.go:301);
  `ModelMetadata{ContextWindow, MaxOutput, Thinking, ThinkingDefault}`
  (config.go:181-186). `config.validate()` ya exige `thinking_default ∈
  thinking` (config.go:447-463) → un thinking_default no vacío implica
  capability declarada.
- **Window check hoy (008-001):** `handleChat` paso 4.5 (proxy.go:560-570)
  usa `req.MaxTokens` (crudo; solo `max_tokens`, NO `max_completion_tokens`)
  y `estimatePromptTokens(body) = len(body)/4`; sin metadata → false.
- **Mapa de activación de thinking por (modelo × provider-path)** — tabla
  verificada, research-token-efficiency.md §5.4 (acceso 2026-08-10), inline:

| Modelo                | Path                      | Parámetro a inyectar                                     | Default nativo                 |
| --------------------- | ------------------------- | -------------------------------------------------------- | ------------------------------ |
| deepseek-v4-flash/pro | zen/go (go-*)             | `reasoning_effort` (pass-through)                          | high (directo) / **OFF (bailian)** |
| deepseek-v4-flash/pro | bailian (qwen)            | `reasoning_effort` + `enable_thinking: true`                 | OFF                            |
| glm-5.2               | zen/go                    | `reasoning_effort` (pass-through)                          | max                            |
| glm-5.2               | bailian                   | `reasoning_effort` (high/max only) + `enable_thinking: true` | ON (dynamic)                   |
| minimax-m3            | zen/go (Anthropic-compat) | zen convierte; thinking type adaptive                    | adaptive                       |
| kimi-k2.7-code        | cualquier                 | **NUNCA inyectar** (`disabled` → ERROR)                        | always-on                      |
| qwen3.7-plus          | bailian                   | `enable_thinking: true` (thinking_budget opcional)         | ON                             |

  Regla general: parámetros no soportados se ignoran silenciosamente
  (OpenAI-compat); única excepción kimi (`disabled` → error).
- **Defaults prescriptivos (010-001 / ADR-003):** deepseek-v4-flash low,
  deepseek-v4-pro high, minimax-m3 adaptive, glm-5.2 medium, kimi-k2.7-code
  always, qwen3.7-plus high. La inyección envía el valor CONFIGURADO (p. ej.
  deepseek-v4-flash → `reasoning_effort=low`, alterando el nativo high).
- **qwen3.7-plus max_output = 131,072 (D8, corregido de 65536):** el clamp y
  el catálogo usan 131072. La aceptación empírica de `max_tokens=131072` por
  bailian queda PENDIENTE de verificación en Phase 4 (deploy) — si bailian
  rechaza >65536, el clamp queda en el valor empíricamente aceptado y TECHDEBT
  registra la discrepancia. Esto es un criterio de deploy, NO cambia el
  contrato 131072 de este spec.
- **Impacto cross-feature verificado — e2e_008001:** con la regla fiel,
  `TestRED_MaxTokensCuentaEnVentana` (window 1000, `MaxOutput: 100`,
  `max_tokens: 400`, margin 0 → hoy espera 400) pasa a 200: 750 +
  min(400,100) = 850 ≤ 1000. El test existente DEBE actualizarse (su premisa —
  el max_tokens crudo cuenta contra la ventana — queda reemplazada por la
  regla fiel). `TestRED_MaxTokensQueCabe` sigue 200 (el upstream ahora recibe
  `max_tokens: 100` en vez de 200). Los demás tests de 008-001 no usan
  max_tokens → no cambian.
- **Colisión de nombre de archivo de test:** `internal/proxy/e2e_010002_test.go`
  YA EXISTE y pertenece a la feature response-cache (010-002-response-cache,
  numeración previa del EPIC-010 de eficiencia — testea X-Mofgw-Cache). Los
  tests de ESTA feature deben usar un nombre distinto (ver Cambios de código).

### Decisiones de diseño (cerradas — no reabrir)

1. **Clamp por modelo pre-routing:** en el proxy (donde vive modelMeta),
   clampear el body a `max_output` del modelo cuando la metadata existe; el
   clamp por provider por-intento del router se mantiene → efectivo
   `min(providerMax, modelMaxOutput)`. kimi-k2.7-code (max_output 32768) con
   max_tokens 384000 → 32768 antes de upstream (mata el 400→502).
2. **Window-check fiel (P2):** `exceedsContextWindow` usa el max_tokens
   ALREADY-CLAMPED → efectivamente `promptTokens + min(rawMaxTokens,
   modelMaxOutput)` vs `window×(1+margin)`. Preserva el fix TECHDEBT #23 (el
   upstream valida prompt+completion contra la ventana real).
3. **Inyección PER-ATTEMPT en el router (provider-aware) — decisión 1.3:**
   cablear modelMeta al router (o un lookup). Cuando el request del cliente NO
   trae effort/thinking explícito, inyectar el `thinking_default` del modelo
   usando el parámetro de activación verificado para el (provider, model)
   (§5.4). NUNCA inyectar para kimi-k2.7-code (always-on; `disabled` → error
   upstream). minimax-m3: nativo adaptive == prescriptivo adaptive → sin
   inyección (documentado). qwen3.7-plus: inyectar `enable_thinking` (ya ON
   nativo → no-op inofensivo). Si el cliente envía effort explícito → RESPETAR
   (sin override).
4. **Stream y no-stream cubiertos:** la reescritura del body no rompe SSE ni
   `stream_options.include_usage` (zen lo inyecta).
5. **nil-safe:** modelo sin metadata → comportamiento actual (sin
   clamp-por-modelo, sin inyección, window check sin cambios → false).
6. **No cambia ruteo/fallback/clasificación:** 4xx sigue no-reintentable; la
   cadena sigue siendo red de seguridad para 429/5xx/timeout.
7. **qwen3.7-plus max_output = 131072 (D8):** contrato de clamp y catálogo;
   verificación empírica bailian como criterio de deploy (Phase 4), sin
   cambiar el contrato.

### Verificaciones pendientes

- **Resolución provider→path (zen/bailian):** el router necesita saber el path
  del provider en cada intento para usar la tabla §5.4. Mecanismo exacto NO
  cerrado por las decisiones: se recomienda campo declarativo
  `providers[].thinking_path` (`""` | `"zen"` | `"bailian"`, default `""` = sin
  inyección) — consistente con I2 (valores de config, nunca inferidos por
  patrón de ID: los IDs reales son acct1, acct2, go-1/2, qwen — un
  patrón de ID es frágil). No bloquea el contrato observable (los tests fijan
  el path del provider). **RESUELTO 2026-08-11 (decisión del dueño): SE
  ADOPTA el knob declarativo `providers[].thinking_path` (`""` | `"zen"` |
  `"bailian"`, default `""` = sin inyección).**
- **Aceptación empírica (Phase 4 / deploy):** qwen3.7-plus `max_tokens=131072`
  por bailian; glm-5.2 `reasoning_effort=medium` por bailian (tabla dice
  high/max only — si se ignora silenciosamente, el wire value enviado es el
  configurado y la discrepancia se registra en TECHDEBT).

## Postcondiciones

Contrato observable: un upstream fake captura el body recibido
(patrón `upstream.gotBody` del harness e2e) — cada postcondición es
determinable por el body que recibe el fake, el status HTTP, o el conteo de
intentos. Los tests NO requieren ver implementación.

1. **P1 — Clamp por modelo (no-stream y stream):** si `model_metadata.<model>
   .max_output > 0` existe para el modelo destino, el body que viaja al
   router está clampeado por modelo: `max_tokens` y `max_completion_tokens`
   del cliente con valor > max_output se reescriben a EXACTAMENTE max_output;
   campos ausentes o ≤ max_output se preservan sin cambios; el resto del body
   queda intacto (mismo patrón de re-encode que 001-004). Con un provider cuyo
   `MaxTokens ≥ max_output`, el upstream recibe el valor clampeado por modelo.
   Ej.: kimi-k2.7-code (`max_output: 32768`) con `max_tokens: 384000` →
   upstream recibe `max_tokens == 32768`.

2. **P2 — Efectivo = min(modelo, provider):** el clamp por modelo (pre-routing)
   y el clamp por provider (001-004, por intento) conviven: el valor que
   recibe el upstream de un intento es `min(max_output_modelo,
   MaxTokens_provider)` cuando ambos existen. Ej.: modelo `max_output: 384000`,
   provider `MaxTokens: 8192`, cliente `max_tokens: 384000` → upstream recibe
   `8192`. Provider sin límite configurado (`MaxTokens ≤ 0`) no relaja el
   clamp por modelo (el efectivo sigue siendo max_output).

3. **P3 — Window-check fiel:** el rechazo por ventana (008-001) usa el
   max_tokens EFECTIVO post-clamp por modelo: rechaza con 400
   `invalid_request_error` y CERO intentos upstream cuando `promptTokens +
   min(max_tokens_cliente, max_output_modelo) > context_window × (1+margin)`;
   no rechaza cuando la suma ≤ límite. Sin metadata (o `max_output == 0`) →
   usa el max_tokens crudo (comportamiento 008-001 actual, TECHDEBT #23
   intacto). El estimador de prompt sigue siendo `len(body)/4`. Ej.: modelo
   `context_window: 1000, max_output: 100`, margin 0, prompt ~750 tokens,
   `max_tokens: 400` → 200 (750+100 = 850 ≤ 1000), y el upstream recibe
   `max_tokens == 100`; con prompt ~1250 tokens y el mismo modelo → 400 sin
   intentos.

4. **P4 — Inyección per-attempt (provider-aware, §5.4):** para un modelo con
   metadata con `thinking_default` no vacío y un par (path, modelo) con
   parámetro de activación verificado: si el body del cliente NO contiene
   ninguno de `{reasoning_effort, thinking, enable_thinking}`, CADA intento de
   la cadena envía al upstream de ESE intento el body con el parámetro de
   activación del (path, modelo) y el valor EXACTAMENTE `thinking_default`
   (string para `reasoning_effort`, `true` para `enable_thinking`).
   Ejemplos: (a) deepseek-v4-flash (`thinking_default: low`) vía zen → el
   upstream recibe `reasoning_effort == "low"`; (b) deepseek-v4-flash vía
   bailian → `reasoning_effort == "low"` Y `enable_thinking == true`;
   (c) qwen3.7-plus (`thinking_default: high`) vía bailian →
   `enable_thinking == true`; (d) glm-5.2 (`thinking_default: medium`) vía
   zen → `reasoning_effort == "medium"`.

5. **P5 — Respeto al effort explícito:** si el body del cliente contiene
   CUALQUIER campo de `{reasoning_effort, thinking, enable_thinking}` (con
   cualquier valor, incluido null), NINGÚN intento inyecta: el upstream recibe
   esos campos tal como los mandó el cliente (passthrough, cero override).
   Ej.: deepseek-v4-flash con `reasoning_effort: "high"` vía zen → el upstream
   recibe `reasoning_effort == "high"` y NO se agrega ningún otro parámetro de
   thinking.

6. **P6 — kimi y minimax:** (a) kimi-k2.7-code: NUNCA se inyecta ningún
   parámetro de thinking (siempre-on; `disabled` → error upstream). Request
   sin effort para kimi → el upstream recibe el body sin parámetros de
   thinking agregados, y el request fluye normal. (b) minimax-m3: sin
   inyección (el nativo adaptive == el prescriptivo adaptive — no hay
   corrección que aplicar; documentado, no es un bug). Request sin effort →
   el upstream recibe el body sin parámetros de thinking agregados.

7. **P7 — Stream y no-stream cubiertos:** las reescrituras (clamp + inyección)
   aplican igual a stream y no-stream y no rompen SSE ni
   `stream_options.include_usage`: (a) stream request sin effort para
   deepseek-v4-flash vía zen → el upstream recibe `reasoning_effort == "low"`
   y, si el cliente no especificó stream_options, `stream_options.
   include_usage == true` (003-001 intacto); el SSE se completa normalmente
   (el primer chunk llega al cliente). (b) stream request con
   `max_tokens: 384000` para kimi → el upstream recibe `max_tokens == 32768`
   y el SSE se completa.

8. **P8 — nil-safe:** modelo SIN metadata → comportamiento actual íntegro:
   sin clamp por modelo, sin inyección, window check idéntico a 008-001
   (max_tokens crudo). Modelo CON metadata pero `max_output == 0` → sin clamp
   por modelo. Modelo con metadata pero `thinking_default == ""` → sin
   inyección. Modelo con `thinking_default` pero par (path, modelo) sin
   parámetro de activación verificado (§5.4) → sin inyección (fallback rule:
   no se adivina). En todos los casos el request fluye como hoy.

9. **P9 — Ruteo/fallback/clasificación sin cambios:** un 400 del upstream
   sigue siendo no-reintentable (el request NO rota al siguiente provider);
   429/5xx/timeout siguen siendo retryable con la cadena como red de
   seguridad. La inyección por intento no altera `classify()`: un intento que
   falla 429 tras recibir el body inyectado falla al siguiente provider con su
   propia inyección. Ej.: deepseek-v4-flash servida por cadena [zen, bailian]:
   zen falla 429 → bailian recibe `reasoning_effort == "low"` +
   `enable_thinking == true`; el primero recibió `reasoning_effort == "low"`
   solamente.

## Invariantes

- I1: Backward compatible — modelo sin metadata (o metadata parcial) →
  comportamiento idéntico al actual donde corresponda (001-004/008-001).
- I2: Los valores inyectados/clampeados provienen de la metadata declarativa
  (007-001/010-001) y de la tabla verificada §5.4 — nunca hardcodeados ad-hoc
  (extiende 007-001 I2).
- I3: El clamp por modelo NO reemplaza el clamp por provider (001-004):
  efectivo = min(modelo, provider); un provider sin límite no relaja el clamp
  por modelo.
- I4: El fix TECHDEBT #23 NO se reabre: la ventana se chequea con el max_tokens
  EFECTIVO (post-clamp por modelo), que es ≤ crudo; el upstream sigue siendo la
  autoridad final (008-001 P4).
- I5: La inyección jamás pisa una decisión explícita del cliente (P5); jamás
  se aplica a kimi (P6); parámetros no soportados se ignoran silenciosamente
  (regla §5.4) salvo kimi.
- I6: La reescritura del body preserva el contrato: campos no relacionados
  intactos, JSON válido, SSE y `stream_options.include_usage` intactos (mismo
  patrón 001-004 / ensureIncludeUsage).
- I7: No cambia ruteo/fallback/clasificación de errores (P9).
- I8: Suite completa `go test ./... -race` verde, cero red externa; `go vet`
  y gofmt limpios.

## Criterios de aceptación

- C1: kimi-k2.7-code con metadata (`max_output: 32768`, `thinking:
  ["always"]`, `thinking_default: always`) y provider con `MaxTokens ≥ 32768`:
  request no-stream con `max_tokens: 384000` → 200 y el upstream fake recibió
  `max_tokens == 32768` (sin 400, sin 502).
- C2: deepseek-v4-flash con metadata (`max_output: 384000`,
  `thinking_default: low`) vía path zen: request no-stream SIN parámetros de
  effort → 200 y el upstream recibió `reasoning_effort == "low"`; el resto del
  body (model, messages, tools si los hay) idéntico al del cliente.
- C3: deepseek-v4-flash vía path bailian, request sin effort → el upstream
  recibió `reasoning_effort == "low"` Y `enable_thinking == true`.
- C4: deepseek-v4-flash con `reasoning_effort: "high"` explícito vía zen → el
  upstream recibió `reasoning_effort == "high"` y NINGÚN parámetro de thinking
  agregado (cero override).
- C5: kimi-k2.7-code sin effort → el upstream recibió el body SIN parámetros
  de thinking agregados (ni `reasoning_effort`, ni `enable_thinking`, ni
  `thinking`).
- C6: minimax-m3 sin effort → el upstream recibió el body SIN parámetros de
  thinking agregados.
- C7: qwen3.7-plus (`thinking_default: high`) vía bailian sin effort → el
  upstream recibió `enable_thinking == true`.
- C8: Window-check fiel — modelo `context_window: 1000, max_output: 100`,
  margin 0: (a) prompt ~750 tokens + `max_tokens: 400` → 200 y upstream con
  `max_tokens == 100`; (b) prompt ~1250 tokens + `max_tokens: 400` → 400
  `invalid_request_error` con CERO intentos upstream; (c) sin metadata, mismo
  body (b) → 200 (backward compatible, 008-001 C3 intacto).
- C9: Streaming — stream request para deepseek-v4-flash sin effort vía zen →
  el upstream recibió `reasoning_effort == "low"` y, sin stream_options del
  cliente, `stream_options.include_usage == true`; el SSE llega al cliente
  completo. Stream kimi con `max_tokens: 384000` → upstream con
  `max_tokens == 32768`, SSE completo.
- C10: Cadena [zen, bailian] sirviendo deepseek-v4-flash con el primero en 429
  → el intento 1 (zen) recibió `reasoning_effort == "low"`; el intento 2
  (bailian) recibió `reasoning_effort == "low"` + `enable_thinking == true`;
  el cliente recibe 200 del segundo (fallback intacto).
- C11: Regresión clasificación — cadena de dos providers, el primero responde
  400 upstream: el cliente recibe 400 y el segundo provider NO recibe ningún
  request (4xx no-reintentable intacto).
- C12: Modelo sin metadata con `max_tokens: 384000` y provider `MaxTokens:
  4096` → el upstream recibe `4096` (clamp por provider actual); con provider
  sin límite → recibe `384000` crudo. Sin inyección, sin rechazo por ventana.
- C13: Suite completa verde: `go test ./... -race`, `go vet`, gofmt limpios.
- C14 (deploy, Phase 4): en prod, kimi-k2.7-code con max_tokens alto ya no
  produce 400→502; deepseek-v4-flash sin effort llega a upstream con
  `reasoning_effort == "low"` (verificado por telemetría/log); qwen3.7-plus
  con `max_tokens: 131072` es aceptado por bailian — si bailian rechaza
  >65536, el clamp queda en el valor empíricamente aceptado y TECHDEBT
  registra la discrepancia (el contrato de este spec es 131072).

## Cambios de código esperados (orientación, no vinculante)

- `internal/proxy/proxy.go handleChat`: tras `ParseChatRequest` (paso 2) y
  ANTES del chequeo de ventana (4.5): si `s.modelMeta[req.Model]` existe con
  `MaxOutput > 0`, clampear el body con `clamp.Request(body,
  int64(md.MaxOutput))` (reutiliza el paquete clamp; sin cambio de firma). El
  body clampeado es el que viaja a `handleStream`/`router.Complete(For)`. El
  chequeo de ventana usa el max_tokens EFECTIVO (P3): `min(raw,
  md.MaxOutput)` — el `ChatRequest.MaxTokens` queda stale tras el clamp (solo
  parsea max_tokens, no max_completion_tokens); el efectivo se computa en el
  call-site (o re-parseando). La posición exacta respecto de
  `responseCacheKey` (paso 2.6) no altera el contrato (el cache exact-match
  sigue keyeando sobre el body crudo del cliente).
- `internal/proxy/proxy.go exceedsContextWindow`: recibe el max_tokens
  efectivo (o el proxy se lo pasa) — sin metadata/`MaxOutput == 0` → crudo
  (sin cambio de firma si se pre-computa; la fórmula del spec es el contrato).
- `internal/router/router.go`: inyección per-attempt. El router necesita (a)
  el lookup de metadata (`Options.ModelMeta map[string]config.ModelMetadata` o
  función lookup, cableada desde main.go — la metadata ya está disponible en
  `cfg.ModelMetadata` al construir Options, main.go:110), y (b) el path del
  provider (ver Verificaciones pendientes: campo declarativo
  `providers[].thinking_path` recomendado; default `""` = sin inyección). En
  cada intento (`complete`/`stream`, tras `clampBody`), si el body ORIGINAL del
  cliente no contiene `{reasoning_effort, thinking, enable_thinking}` y el
  (path, modelo) tiene parámetro verificado (§5.4, tabla como dato puro con
  ref a research), inyectar el parámetro con valor `ThinkingDefault` (string /
  bool). Componer con `ensureIncludeUsage` (stream): inyección por intento
  sobre `attemptBody`, que ya incluye include_usage.
- `internal/clamp/clamp.go`: sin cambios de firma; se reutiliza `Request` para
  el clamp por modelo (mismo re-encode preserva campos).
- `cmd/mofgw/main.go`: cablear `cfg.ModelMetadata` (y el path por provider si
  se adopta el knob) a `router.Options` al construir el router.
- `internal/config/config.go` (+ `config.example.yaml`): SE ADOPTA el knob
  declarativo `providers[].thinking_path` (decisión del dueño 11 Ago).
  Documentar los `thinking_default` prescriptivos
  y el `max_output: 131072` de qwen3.7-plus (este último ya en scope de
  010-001 C7).
- Tests: **archivo NUEVO `internal/proxy/e2e_010002_requestpath_test.go`** —
  el nombre `e2e_010002_test.go` YA está ocupado por la feature response-cache
  (numeración previa; NO sobrescribir). Patrón harness existente
  (`upstream.gotBody`, `buildMultiClient`, `h.proxySrv.SetModelMetadata`,
  `SetContextMargin`) + fakes con ID/path controlado por el test, para C1-C13.
  **Actualizar `e2e_008001_test.go`:** `TestRED_MaxTokensCuentaEnVentana`
  cambia de contrato (400 → 200 con la regla fiel; ver Contexto) — actualizar
  a la nueva expectativa (p. ej. prompt mayor o max_tokens ≤ max_output para
  el caso 400, o aserción del clamp). Tests unitarios de router para la tabla
  de inyección (P4-P6) y de clamp por modelo si se factoriza un helper.

## Fuera de alcance

- Tokenizer real (contar tokens exactos) — estimación rough por diseño
  (008-001 P2).
- `thinking_budget` para qwen3.7-plus (opcional; no se inyecta — solo
  `enable_thinking: true`).
- Aceptación de valores por el upstream (p. ej. glm medium / qwen 131072 vía
  bailian) — verificación empírica en Phase 4; el wire value enviado es el
  configurado y la discrepancia se registra en TECHDEBT.
- Enforcement del default prescriptivo en el CLIENTE — el gateway solo inyecta
  cuando el cliente no especifica effort (P5); los runtimes siguen mandando lo
  que quieran.
- Cambios de ruteo/fallback/clasificación, cooldown, health, degradación —
  intactos (P9/I7).
- Cambios de `context_window` (1,000,000 vs 1,048,576) — verificación empírica
  pendiente, fuera de scope (010-001).
- Métricas nuevas para clamp/inyección — opcional logs debug
  (`model_clamp`, `thinking_injected`), sin contadores nuevos.
- Config del cliente opencode (models.dev) — fuera de nuestro control.
- Enforcement de visión por `modality` — declarado en catálogo (010-001), no
  validado en el request path.

## Cambios

- 2026-08-11: spec aprobado (GVR). Decisión del dueño: knob `providers[].thinking_path` ADOPTADO (declarativo, default "" = sin inyección). Test file: `e2e_010002_requestpath_test.go` (colisión con e2e_010002_test.go de response-cache). POST-AUDIT mandatorio: `TestRED_MaxTokensCuentaEnVentana` (e2e_008001) cambia 400→200 con la regla fiel — se aplica ANTES del GREEN.