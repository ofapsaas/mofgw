# activeContext.md — Contexto activo de mofgw

> Memory Bank: estado actual, decisiones recientes, próximos pasos, deuda conocida.
> Última actualización: 2026-08-12 (cdad-scribe, EPIC-011-mofgw-odoo CERRADO).

## Estado del programa

**✅ Programa mofgw 10/10 epics DONE (001-010) + EPIC-011 (mofgw-odoo) CERRADO 12 Ago 2026 — 9/9 features done + integración E2E verificada + closure completado. mofgw es el único proveedor de IA de Odoo (reemplazo total de OpenAI).**

| Epic | Features | Status | Commit evidence |
|------|----------|--------|-----------------|
| EPIC-001 (core) | 7 features | ✅ DONE | e426f15d |
| EPIC-002 (resiliencia) | 4 features | ✅ DONE | 5966c5f4 |
| EPIC-003 (cache) | 1 feature | ✅ DONE | bb6367b→e5a9a16 |
| EPIC-004 (escala) | 2 features | ✅ DONE | (05 Ago) |
| EPIC-005 (ops) | 5 features | ✅ DONE | (05 Ago) |
| EPIC-006 (observabilidad) | 2 features | ✅ DONE | 119fe9d→2846c12 |
| EPIC-007 (catálogo) | 3 features | ✅ DONE | (06 Ago) |
| EPIC-008 (contexto) | 3 features | ✅ DONE | (06 Ago) |
| SEC-001 (hardening) | 4 fixes | ✅ DONE | b368656 |
| EPIC-009 (contexto-análisis) | 3 features | ✅ DONE | da8446c (E2E cross-feature) + closure-009.md |
| EPIC-010 (catálogo-fiel) | 2 features | ✅ DONE | (11 Ago, deploy prod) |
| EPIC-011 (odoo) | 001-009 DONE (9/9) | ✅ DONE + INTEGRACIÓN + CLOSURE | closure-011.md |

**Suite:** 19 paquetes, **499 tests verde con `-race`** (mofgw Go; el módulo Odoo mofgw_ai tiene 14 tests de módulo que pasan en VPS). vet limpio.
**EPIC-011 (mofgw-odoo) CERRADO — 9/9 features + integración E2E verificada:** Odoo 19 enterprise usa mofgw como único proveedor de IA (reemplazo total de OpenAI). Lado Go: responses (001), structured output (002), tool-calling (003), file-attachments (004), web-search grounded (005), embeddings (006). Lado Odoo (módulo local mofgw_ai): embedding-vector (007), odoo-provider (008, monkeypatch in-place). staging-enterprise (009). **E2E real verificado:** Odoo (staging-instance) → provider mofgw → túnel → mofgw Go local → deepseek-v4-flash, /v1/responses 200 "PONG", 0 llamadas a api.openai.com.
**Deploy:** systemd user service activo en puerto 3369, providers reales (acct1, acct2, qwen/bailian, zen free).

## Decisiones recientes (cronología inversa)

### 12 Ago 2026 — Epic 011-mofgw-odoo cerrado (mofgw como proveedor de IA de Odoo)

### Decisiones relevantes

- **EPIC-011 (mofgw-odoo) CERRADO — 9/9 features done + integración cross-feature E2E verificada + closure completado.** El epic hizo que mofgw sea el **único proveedor de IA** de una instancia Odoo 19 enterprise (reemplazo total de OpenAI), sin que Odoo se entere del cambio. Cadena entregada: lado Go (`/v1/responses` [001], structured output [002], tool-calling [003], file attachments [004], web-search grounded [005], `/v1/embeddings` [006]) + lado Odoo (módulo `mofgw_ai`: vector 384 [007], provider [008]) + infra staging enterprise [009]. Programa mofgw queda **10/10 epics done (001-010) + EPIC-011 completo**. Closure: `docs/epics/closure-011.md`, ADRs 004-009.
- **Integración E2E real verificada (integration-011.md):** `Odoo enterprise (staging-instance) → provider mofgw → túnel odoo-host:3369 → mofgw Go local → deepseek-v4-flash`, `curl /v1/responses` con key `<test-key>` → **HTTP 200 "PONG"**; log mofgw confirmó traducción Responses→chat interna. 14/14 tests del módulo Odoo. 0 llamadas a `api.openai.com`. Suite Go **499 tests `-race`** en 19 paquetes.
- **Decisiones transversales del epic → ADR:** (1) **"el cliente nunca elige"** (ADR-008) — mofgw decide el modelo de embeddings por cliente (006), gating opt-in de web_search (005), dimensión única de Odoo (008); (2) **contrato alineado al parser real de Odoo** (ADR-009) — forma del `output[]`, solo-texto de web search, tests contra `llm_api_service.py` real, no la spec OpenAI.
- **8 criterios de aceptación del epic verificados** (integration-011.md): 9/9 done, E2E chat/structured/RAG/file/web, modelo forzado por cliente, 0 llamadas a OpenAI, suite `-race` verde.

### Deuda técnica detectada (se llevó al closure-011)

- **`ir_actions_server.AI_PROVIDER` (D6):** el path de server actions (`state='ai'`) sigue apuntando a openai/gpt-4.1. Deuda documentada; requiere parchear `ir_actions_server.py:26-27` en epic futuro.
- **Embeddings reales sin verificar contra Ollama desplegado:** el E2E de embeddings usó mock; falta verificación empírica con un Ollama accesible desde el VPS.
- **Reindexación de sources:** si una instancia tenía chunks 1536-dim, reindexar tras el redimensionado (paso operativo en README de mofgw_ai).
- **Audio out (whisper/realtime):** deuda del epic — Odoo con mofgw no tendrá voz hasta epic posterior.

### Próximo paso en cola

- **NINGUNO en mofgw-odoo — epic 011 cerrado.** Programa mofgw completo (10/10 epics + 011). Restos operativos: deuda D6 (server actions), verificación empírica de embeddings reales con Ollama, y criterios del programa general (E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes). Decidir: próximo epic, feature standalone, o cerrar.

### 12 Ago 2026 — Feature: 011-008-odoo-provider (epic 011-mofgw-odoo) — ÚLTIMA, epic 9/9

### Decisiones relevantes

- **Feature 011-008-odoo-provider DONE — registro de mofgw como provider de IA de Odoo 19 (módulo local `mofgw_ai`, ÚLTIMA feature del epic 011 → epic 9/9 done).** Del lado Odoo (0 cambios Go). El módulo registra mofgw reutilizando el flujo openai de `LLMApiService` (formato `/v1/responses`, tool call openai) apuntando `base_url` a mofgw. Verificado en VPS (staging-vps/staging-instance): **13/13 tests pasan** (4 de 007 + 9 del provider).
- **Mecanismo D1 — monkeypatch in-place (no subclase):** el core instancia `LLMApiService` por nombre y comparte `PROVIDERS` por referencia. `mofgw_ai/__init__.py` hace `PROVIDERS.append(Provider('mofgw', 'mofgw', 'all-minilm', [5 llms]))` + parchea in-place `__init__` (base_url), `_get_api_token` (config→env→UserError), `_request_llm` (delega a `_request_llm_openai`), `_build_tool_call_response` (formato openai). Cada parche = guard+delegate al original para openai/google (sin regresión I2). **Doble guard de idempotencia** (`if not any(p.name=='mofgw')` + flag `_mofgw_patched`). **→ ADR-007.**
- **D3 — override obligatorio de `ai.embedding.embedding_model`:** `PROVIDERS.append` NO actualiza `EMBEDDING_MODELS_SELECTION` (snapshot separado) → re-declara `embedding_model = fields.Selection(selection_add=[('all-minilm','All-minilm')])` con `ondelete={'all-minilm':'cascade'}`. El `embedding_model` del Provider mofgw es exactamente `'all-minilm'` → embeddings 384-dim alineados con la columna `vector(384)` de 007 (I4).
- **D4/D5 — config por instancia:** `ir.config_parameter` `ai.mofgw_url` + `ai.mofgw_key`, fallback env `ODOO_AI_MOFGW_TOKEN`; `res.config.settings` hereda + view. **URL default `http://127.0.0.1:3369/v1`.** Masking key vía `widget="password"` (patrón core).
- **Review APPROVE (qwen3.7-plus):** **0 bloqueantes.** Fix #2 APLICADO (spec password via widget); #1/#3 DESCARTADOS; FYI #4 (E2E staging). Auditoría: 10/11 postcondiciones con test (P11 E2E deuda). Monkeypatch correcto, doble guard idempotencia, ondelete cascade, seguridad key adecuada.
- **3 bugs de test corregidos en GREEN:** openai también usa `function_call_output`; `get_values()` devuelve strings; `requests` normaliza `method` a `'post'`.
- **EPIC 011 COMPLETO — 9/9 done (001-009).** Entra a integración cross-feature y closure — coordinar vía `cdad-epic`.

### Deuda técnica detectada

- **`ir_actions_server.AI_PROVIDER` (D6, out of scope):** el path de server actions queda fuera; el path principal cubierto es `ai.agent`. Deuda del epic para integración/closure.
- **P11/C7 E2E no verificada (FYI):** `request_llm` real contra mofgw en staging — ejecutar manualmente antes de cerrar 011.
- **`_to_open_ai_tool_schema`** no transforma para provider != 'openai'; mofgw traduce en su capa. Verificar si algún upstream exige la forma openai estricta.
- **Monkeypatch global (ADR-007, Media):** patrón no soportado de Odoo — re-evaluar si el core cambia `LLMApiService`/`PROVIDERS` en futuras versiones.

### Próxima feature en cola

- **NINGUNA — epic 011 completo (9/9 done).** Siguiente: **integración cross-feature + closure del epic 011-mofgw-odoo** vía `cdad-epic` (E2E integral de los 8 endpoints Go + módulo Odoo en staging, ejecutar P11/C7, resolver deuda D6).

### 12 Ago 2026 — Feature: 011-007-embedding-vector (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-007-embedding-vector DONE — alineación de la dimensión del vector de embeddings en Odoo (módulo local `mofgw_ai`).** Overridea `ai.embedding.embedding_vector = Vector(size=384)` (reemplaza el `Vector(size=1536)` hardcodeado en ai_embedding.py:32) y redimensiona el schema en PostgreSQL. `_get_dimensions()` ya lee `size` en runtime (ai_embedding.py:37) → 384 fluye automático a los 2 usos (cron ai_embedding.py:112, RAG ai_agent.py:597) sin tocar el core. **Verificado end-to-end en VPS (staging-vps, instancia enterprise `staging-instance`):** columna `vector(384)` + índice ivfflat recreado automáticamente en install, 4 tests del módulo pasan. Sin cambios Go. Es la 7ma feature del epic (**8/9 done: 001-007 + 009**).
- **Corrección de diseño mayor (verificada contra source Odoo 19) — redimensionado del schema vía `pre_init_hook` (install) + `migrations/1.0/pre-migrate.py` (upgrade), NO `post_init_hook` ni solo `migrations/`.** Razones verificadas: (a) `post_init_hook` corre DESPUÉS del schema sync (`init_models`, loading.py:186→235) → dropea el índice y nada lo recrea (I4 roto); (b) `post_init_hook` NO corre en upgrade (`update_operation != 'install'`, loading.py:231-238) → la columna PG queda 1536 (P6 roto); (c) `migrations/` solo corren en `'to upgrade'` (migration.py:151) → no corren en install fresco. `pre_init_hook` + `pre-migrate` corren ANTES de `init_models` → tras el DROP+ALTER, `apply_to_database`/`check_indexes` recrea el índice automáticamente. → **ADR-006**.
- **Firma del hook:** `def redimension_embedding_vector(env)` — Odoo 19 invoca pre_init_hook con UN argumento (loading.py:177/235); el hook vive en `mofgw_ai/__init__.py`.
- **Idempotencia explícita (P9, bloqueante #3 review):** guarda que consulta `format_type` y skip si ya es `vector(384)`. Bloqueantes #1/#2/#3 de la review **resueltos** (pre_init_hook, pre-migrate.py, guarda); opcionales #5 README / #6 migrations / #7 typo **aplicados**; #4 parcial (B6 idempotencia), #8 descartado.
- **README documenta el prerequisito operativo (P10/D3/C5):** una `ai_embedding` con chunks 1536-dim hace fallar el `ALTER`; purgar antes de migrar. Paso operativo documentado.
- **Infraestructura resuelta en VPS:** pgvector instalado en staging-vps (root); instancia Odoo enterprise `staging-instance` creada (db `staging_mofgw` UTF8 + vector). `ai` + `ai_app` + `mofgw_ai` instalados.

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante, P10/D3):** comportamiento real del `ALTER ... TYPE vector(384)` en una instancia con chunks 1536-dim. Paso operativo de purga documentado; staging vacío sin impacto.
- **Comparte módulo con 011-008 (D4):** `mofgw_ai` es el módulo compartido; 011-008 agrega el registro del provider mofgw/Ollama.
- **Patrón reusable documentado (ADR-006):** redimensionar una columna `vector` en Odoo 19 requiere pre_init_hook (install) + pre-migrate (upgrade) ANTES de `init_models`, con guarda de idempotencia y DROP del índice ivfflat antes del ALTER.

### Próxima feature en cola

- **011-008-odoo-provider** (epic 011-mofgw-odoo): registrar mofgw en `PROVIDERS` + `base_url`/key en `LLMApiService`, config por instancia. Mismo módulo local `mofgw_ai` (D4 de 007). Queda SOLO 008 pendiente (001-007 done; 009 staging-enterprise ya DONE — **8/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-006-embeddings (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-006-embeddings DONE — `POST /v1/embeddings` en mofgw, forward a Ollama (endpoint que Odoo 19 usa en `_request_llm_embedding`).** mofgw actúa como gateway: recibe el request de Odoo, **fuerza el modelo de embeddings del cliente** (principio "el cliente nunca elige", P3 — el `model` que manda Odoo se IGNORA por completo, no llega a Ollama ni al envelope), lo forwardea al Ollama configurado en `embeddings.base_url` y devuelve el vector OpenAI-compatible. Segunda llamada saliente del proxy (tras DDG en 005), con un **cliente HTTP dedicado**. Suite completa **499 tests `-race`** verde en **19 paquetes** (paquete nuevo `internal/embeddings` + 18 tests feature), go vet limpio. Commits: `ca0931f` RED, `7c3b5d7` GREEN. Es la 6ta feature del epic 011 (**7/9 done con 009**).
- **Nuevo paquete `internal/embeddings` — cliente HTTP dedicado (patrón `websearch.Client` de 005, aplica ADR-005):** `provider.Client` NO es reusable para embeddings (`Complete`/`Stream` hardcodean `/chat/completions`, I7). `embeddings.Client` tipado (interfaz `Embed(ctx, body) → raw`, sin reflexión) y el concreto `Ollama` hace `POST base_url+"/embeddings"`. Nunca habla con `api.openai.com` (I2). Setter `SetEmbeddings(baseURL, apiKey)` (global) + `SetClientEmbeddingsModel(clientID, model)` (mapa inmutable post-set, patrón SetBudget). Router, providers y `provider.Client` intactos (I1, I7).
- **D1 — dimensions passthrough (hallazgo descubrimiento verificado):** mofgw fuerza el modelo por cliente pero **NO valida el `dimensions` de Odoo** (hardcodeado en 1536 en ai_embedding.py:32); lo deja pasar a Ollama, que **SÍ acepta `dimensions` y trunca** si el modelo lo soporta. El vector real es la dimensión **nativa** del modelo (all-minilm = 384). La alineación de dimensión es **config manual en Odoo** (feature 011-007), no validación en mofgw (I4). Cambiar de modelo de embeddings ⇒ reindexar sources en Odoo (riesgo operativo, plan-011:66). `input` viaja byte-idéntico (P5).
- **D2 — cliente sin `embeddings.model` → 400 fail-fast:** sin default global silencioso; `error.message == "no embeddings model configured for client"`. D3 — `base_url` **global** (un Ollama por instancia mofgw), modelo **por cliente**. D4 — auth de Ollama sin key por default, `embeddings.api_key_env` **opcional**. D5 — pricing por **tokens del input** (`prompt_tokens`, mismo flujo `recordCacheTokens`); modelo ausente de `pricing:` → costo 0; si se agrega a `pricing:` pasa a costar automáticamente. Headers `X-Usage-*` con `setUsageHeaders`. Budget por cliente aplicado (P8).
- **Review APPROVE (qwen3.7-plus):** **0 bloqueantes.** Opcional **#2 APLICADO** — `parseEmbeddingsUsage` reutiliza `provider.Usage` directo; opcional **#6 APLICADO** — frontmatter del spec. #1/#3/#4 **DESCARTADOS con motivo**; FYI #5 (límite 4MB documentado). Auditoría: **10/10 postcondiciones con test**, ninguno sobrante, cero dependencia interna.

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante):** compatibilidad real del shape de embeddings (`input` string-vs-array, `data[].embedding`, `dimensions`/truncado) contra el Ollama real y `_request_llm_embedding` de Odoo 19 (tests usan mock upstream).
- **Riesgo operativo documentado (plan-011:66):** cambiar el modelo de embeddings a una dimensión distinta a la nativa (all-minilm=384) ⇒ reindexar sources en Odoo. Alineación manual (011-007).
- **Dependencia saliente a Ollama (nueva, aplica ADR-005):** segunda llamada saliente del proxy (tras DDG). Timeout 30s por intento, body upstream limitado a 4MB, 429 de Ollama no-reintentable → 502 (P9).

### Próxima feature en cola

- **011-007-embedding-vector** (epic 011-mofgw-odoo): alineación de la dimensión del vector de embeddings en Odoo (config manual, arranca desde el hallazgo D1 de 006). Quedan 007-008 pendientes (001-006 done; 009 staging-enterprise ya DONE — **7/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-005-web-search (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-005-web-search DONE — grounded search server-side en `/v1/responses`.** Reemplaza el rechazo incondicional D3 de 003 (cualquier tool `type != "function"` → HTTP 400) por la **resolución real** del caso `web_search_preview`: detectar → buscar en DDG → inyectar los resultados como system message prependido → **remover** la tool del upstream → item message. mofgw **emula** el grounded search server-side porque Odoo NO maneja el ciclo `web_search_call` (verificado: Odoo solo lee `text` del item `message`). Suite completa **481 tests `-race`** verde en **18 paquetes** (paquete nuevo `internal/websearch` + 16 tests feature), go vet limpio. Commits: `dfea7b1` RED, `ae10d86` GREEN.
- **D1 — mofgw emula grounded search (primera llamada saliente del proxy → ADR-005):** detectar `web_search_preview` → ejecutar DDG → inyectar resultados como system message **prependido** → **QUITAR** `web_search_preview` de los tools upstream → item message normal. Sin ciclo `web_search_call`. Decisión arquitectónica nueva → **ADR-005**.
- **D2 — query = último mensaje user (determinista):** concatenación de los text de las parts input_text del último item message con role=="user". Descartada la alternativa de 2 round-trips.
- **D3 — cliente DDG propio en Go, sin dependencias:** `internal/websearch` scrapea `html.duckduckgo.com/html/?q=` → top-N `{Title,URL,Snippet}`, con fallback a Instant Answer API. Solo stdlib (regex + html.UnescapeString + net/http). Contrato `websearch.Client` desacoplado del parser.
- **D4 — inyección como system message:** `[Web search results for "<query>"]` + N items numerados `title|url|snippet`, N = max_results (default 3). Sin url_citation (Odoo solo lee text).
- **D5 — gating `web_search.enabled:false` default (opt-in):** false → 400 conservado de 003 (sin regresión); true → flujo web search. Fallo de DDG = **best-effort** sin grounding (nunca 4xx/5xx al cliente). Tool type!=function no-web-search sigue 400 en ambos modos.
- **Despacho 3 vías** en la rama que rechazaba: (a) web_search_preview + enabled:true → flujo; (b) web_search_preview + enabled:false → 400; (c) type!=function no-web-search → 400. Infra: WebSearchConfig (enabled/max_results/timeout), setter **tipado** `SetWebSearch(enabled bool, client websearch.Client)`, wiring en main.go. **I1/ADR-004 respetado.**
- **P8 — cache excluido para web search:** un request web search activo **nunca** se sirve ni almacena en el response cache aunque temperature==0 (resultados DDG cambian; P8_CacheExcluidoParaWebSearch verifica 2 llamadas al upstream con mock A→B).
- **Review APPROVE (qwen3.7-plus):** **1 bloqueante RESUELTO + 1 opcional APLICADO.** Bloqueante #1 — setter/campo `any` + 47 líneas de reflect (fallo silencioso, viola 2 laws) → **reemplazado por la interfaz tipada `websearch.Client`** (mock devuelve `[]websearch.Result`; se eliminó el reflect, el import y reflectFieldString). Opcional #2 APLICADO — `html.UnescapeString` stdlib. #3/#4/#5 DESCARTADOS con motivo. FYI #6/#7 sin acción. Auditoría: 8/8 postcondiciones con test.

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante):** soporte real de DDG (scrape + fallback Instant Answer) con provider real — tests usan mock (I6).
- **Dependencia saliente a DDG (nueva, ADR-005):** primera llamada saliente del proxy a un servicio no-provider. Best-effort sin errores nuevos (I2), query solo como query parameter (sin SSRF), body limitado a 4MB. Vigilar disponibilidad/rate limits de DDG en prod.
- **Riesgo del epic vigente (sin cambio):** Odoo 19 usa Responses API en estado "preview". Web search queda enabled:false por default hasta opt-in en config de prod.

### Próxima feature en cola

- **011-006-embeddings** (epic 011-mofgw-odoo): forward de embeddings a Ollama (modelo por cliente, pricing). Quedan 006-008 pendientes (001-005 done como troncos; 009 staging-enterprise ya DONE — **6/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-004-file-attachments (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-004-file-attachments DONE — traducción de las parts de archivo de Odoo (`input_file`/`input_image`) en `/v1/responses`.** Reemplaza la rama de rechazo P9 de 001 (400 "file attachments not yet supported") por la traducción real de las parts del `input[]` Responses al formato chat-completions que hablan los providers upstream. Todo el cambio vive en `internal/proxy/responses.go` (ADR-004/I1: router, providers y `ParseChatRequest` intactos). Suite completa **465 tests `-race`** verde en 17 paquetes (452 → 465, 12 tests feature nuevos en 6 bloques + POST-AUDIT en 001 y 003 + 1 fix review), go vet limpio. Commits: `7fce35b` RED, `69ddae1` GREEN.
- **D1 — decisión POR MENSAJE string-vs-array (P3):** un mensaje del `input[]` con SOLO parts `input_text` → `content` = **string** (concatenación de los `text`, exactamente como 001/003 — sin regresión en los caminos de texto plano); un mensaje con AL MENOS una part `input_image`/`input_file` → `content` = **array** con un item por part del input, en orden de aparición (I3). La decisión es por item, no por request: en un mismo `input[]` conviven mensajes string y array (verificado por `P3_MensajesMixtosPorMensaje`). El content-array se aplica SOLO a items `message`; los items `function_call`/`function_call_output` del ciclo tool-calling de 003 quedan intactos (P4).
- **D2 — mapeo de parts:** `input_text` → `{"type":"text","text":T}`; `input_image` → `{"type":"image_url","image_url":{"url":U,"detail":D?}}` (detail passthrough con omitempty: ausente → el campo no se emite, misma semántica que `strict` de 002/003); `input_file` → `{"type":"file","file":{"file_data":D,"filename":F}}` (part estilo OpenAI, sin detail). Data-URIs viajan como strings tal cual, sin re-marshal (I2, byte-identidad). Parts de tipo desconocido se dropean conservando el orden de las conocidas (consistente con el string-content que ignora lo que no es `input_text`).
- **D3 — sin gating por modality / PDF honesto (P6):** mofgw traduce y reenvía; NO conserva el 400 local, NO filtra por modality, NO convierte PDF. El `modality` es declarativo (solo catálogo `/v1/models`); el router elige por `req.Model`. Si el modelo elegido no soporta vision/archivos, el 4xx upstream se propaga **no-reintentable** (patrón D5 de 002). Verificación empírica del soporte PDF del provider real queda como **desviación no bloqueante**.
- **Sin cambio en salida ni en contexto (P8/P9):** la traducción de salida queda intacta (item `message`/`function_call` de 001/003); `estimatePromptTokens = len(body)/4` no se toca — las data-URI base64 inflan `len(body)` pero la ventana descuenta automáticamente los tokens sobreestimados (P8, sin cambio de código).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcionales **#1 APLICADO** (eliminar dead code `inputPart` de 001 sin callers tras POST-AUDIT) y **#3 APLICADO** (subtest `P3_PartTipoDesconocidoDropeada`: part `input_unknown` en mensaje content-array → 2 items en orden, la desconocida dropeada). #4/#5 **DESCARTADOS con motivo**, FYI #6 sin acción. Auditoría test↔postcondición: **9/9 postcondiciones con test** (P7/P9 por tests untouched de 001/003), ninguno sobrante, cero dependencia interna (AP-14).

### Deuda técnica detectada

- **Verificación empírica pendiente (desviación no bloqueante):** soporte real de las parts `file` (PDF) y `image_url` por los providers upstream — mofgw traduce y propaga el 4xx no-reintentable (D5 de 002) si el provider no las soporta. Verificar con provider real.
- **Riesgo del epic vigente (plan-011, sin cambio):** Odoo 19 usa Responses API en estado "preview"; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py:283-304`, formato de parts verificado en el spec).

### Próxima feature en cola

- **011-005-web-search** (epic 011-mofgw-odoo): habilita `web_search_preview` / tools no-function (hoy 400 conservado como D3 de 003 y P7 de 004). Quedan 005-008 pendientes (001-004 done como troncos del epic; 009 staging-enterprise ya DONE — **5/9 done**). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-003-tool-calling (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-003-tool-calling DONE — traducción del ciclo tool-calling de Odoo (tools ↔ function_call ↔ function_call_output) en `/v1/responses`.** Reemplaza la rama de rechazo P7 de 001 (400 "tool calling not yet supported") por la traducción real bidireccional en las tres direcciones: (1) entrada `body["tools"]` (formato Responses plano) → `tools` chat-completions anidado bajo `function`; (2) salida `choices[0].message.tool_calls` → items `function_call` del `output[]`; (3) entrada item `function_call_output` → mensaje `role:"tool"`. **Quién maneja el ciclo: Odoo** ejecuta las tools por su cuenta y arma el siguiente request; mofgw solo traduce (stateless, sin estado de sesión — cada request es una traducción puntual del `input[]` completo). Suite completa **452 tests `-race`** verde en 17 paquetes (426 → 452), go vet limpio. Commits: `cea354e` RED, `fefb55c` GREEN.
- **D1 — `call_id` = eco directo del id upstream (base del round-trip):** el `call_id` del item `function_call` de salida (P6) == `tool_calls[i].id` upstream; el `id` estable del item (P11 de 001, `responsesID`) va APARTE (no == call_id, P8). Solo así Odoo devuelve `function_call_output.call_id` == ese mismo id y mofgw lo mapea al `role:"tool"` con `tool_call_id` correcto (P4/I4). En entrada, el `tool_call.id` del assistant == `call_id` del item `function_call` (fallback al `id` si no trae `call_id`), cerrando el round-trip en ambas direcciones.
- **D2 — solo items `function_call` cuando hay tool_calls (cero items message):** con `tool_calls` no vacío, `output[]` contiene EXACTAMENTE un item `function_call` por `tool_calls[i]` y NINGÚN item `message` (Odoo ignora el texto del message en esa rama, `elif not has_tool_calls`). Con `tool_calls` vacío/ausente → item `message` intacto (P7, P10 de 001). Shape verificado parseable por el snippet real `_request_llm_openai_helper` (lee `name`/`arguments`/`call_id` → `to_call == [(name, call_id, arguments)]`).
- **D3 — `type != "function"` → 400 conservado (lo resuelve 005):** si `tools` contiene CUALQUIER tool de `type != "function"` (incluido `web_search_preview` y el caso mixto function+no-function), HTTP 400 "tool calling not yet supported" — el chequeo ocurre ANTES de traducir. 003 traduce solo los `type == "function"`. Rechazo de tool JSON malformado separado: **"invalid tool definition"** (opcional #3 de la review, APLICADO).
- **Byte-identidad I3 e invariantes sostenidos:** `parameters` de cada tool viaja como `json.RawMessage` y se serializa verbatim (byte-idéntico al input, sin re-marshal); `arguments` del item de salida es el string JSON crudo parseable por `json.loads` (I2); `strict` con omitempty (ausente → no se emite, misma semántica que D2 de 002). `tools`/`parallel_tool_calls` viajan como passthrough en `chatBodyMap` (inyectados antes del `Marshal`) y sobreviven clamp e inyección de thinking (P9), con cache exact-match separada por endpoint que cambia de key con los tools.
- **provider.go: solo type definitions (I1/ADR-004 sin romper):** `ChatToolCall` (id/type/function{name,arguments}) y `ToolCalls []ChatToolCall` en `ChatMessage` (`omitempty`). NO modifica `ParseChatRequest`, router ni providers; `responsesInputItem` discriminado por `type` (message / function_call / function_call_output) cubre los items mixtos del `input[]`. Todo el cambio de la feature vive en `internal/proxy/responses.go`.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcional #3 **APLICADO** — unmarshal fallido de tool → "invalid tool definition" (rama separada del 400 "tool calling not yet supported" que queda solo para type!=function). Opcionales #1 (agrupar function_call adyacentes en un único assistant multi-tool_call) y #2 (subtest tool_calls_vacio no ejercita `[]`) **DESCARTADOS con motivo**. FYI #4/#5 sin acción. Auditoría test↔postcondición: P1-P10 todos con test (10/10), ninguno sobrante, cero dependencia interna (AP-14). POST-AUDIT en 001: subtest `tools` flip 400→200 (traducción verificada), `web_search_preview` conserva 400 (D3).

### Deuda técnica detectada

- **Agrupación de `function_call` adyacentes en un único mensaje assistant multi-tool_call** (hardening futuro): el contrato especifica 1:1 (un item function_call → un mensaje assistant con un tool_call), cubriendo el caso de un solo tool (el común en Odoo); con `parallel_tool_calls:true` y varios adyacentes, hoy se generan N mensajes assistant separados. Funcional para Odoo (parsea tool_calls uno a uno); documentado como out-of-scope en el spec, optimización de implementación a futuro.
- **Verificación empírica pendiente (desviación no bloqueante):** aceptación de `strict` en tools por los providers reales upstream — el diseño pasa `strict` tal cual; si un provider lo rechaza, mofgw propaga el 4xx no-reintentable (patrón D5 de 002, sin degradación). Verificar con provider real.
- **Deuda del epic vigente (sin cambio):** streaming SSE del Responses API (deuda del epic), y `web_search_preview`/tools no-function que hoy son 400 y los resuelve la feature 005.

### Próxima feature en cola

- **011-004-file-attachments** (epic 011-mofgw-odoo): habilita las parts `input_file`/`input_image` del `input[]` (hoy 400 "file attachments not yet supported", P9 de 001). Quedan 004-008 pendientes (001/002/003 done como troncos del epic; 009 staging-enterprise ya DONE). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-002-structured-output (epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-002-structured-output DONE — `text.format.json_schema` → `response_format` en la ENTRADA de `/v1/responses` (structured output, AI Field Fill de Odoo).** Reemplaza el rechazo P8 de 001 (400 "structured output not yet supported") por la traducción real: cuando Odoo manda `text.format = {"type":"json_schema",...}`, mofgw inyecta `response_format = {"type":"json_schema","json_schema":{...}}` en el body chat-completions que delega al provider. **Alcance estricto: solo la entrada; la salida NO cambia** — sigue el shape P10 de 001 (`output[0]` item `message`, `content[0].text == choices[0].message.content`), el JSON estructurado viaja como texto normal del `content`, verificado contra `_request_llm_openai_helper`. Suite completa **426 tests `-race`** verde en 17 paquetes (413 → 426, 7 tests nuevos + POST-AUDIT), go vet limpio. Commits: `6223207` RED, `de162e1` GREEN.
- **Mapeo D1 — `response_format` estructural (no plano):** `text.format` (4 campos planos de Odoo) → `response_format = {"type":"json_schema","json_schema":{"name","schema","strict"}}`. El `schema` viaja como `json.RawMessage` (responses.go:151,158) y se serializa verbatim → **byte-identidad I3** sostenida en toda la cadena (el clamp opera sobre `map[string]json.RawMessage`, `response_format` es passthrough opaco que no desarma). `strict` con `omitempty` (D2): presente → valor exacto; ausente → campo no se emite.
- **D3/D4/D5 (decisiones del brainstorm mantenidas):** solo `type == "json_schema"` se traduce; el resto de `type` o un format malformado conserva la rama P8 (400 "structured output not yet supported") (D3). `temperature` tal cual, NO forzado a 0 por tener structured output (D4). Sin degradación a `json_object`: provider que no soporta `response_format` → mofgw propaga el 4xx upstream no-reintentable, sin fallback de menor capacidad (D5, `router.Complete` → `handleChainError` → `absorb.Respond`).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** **0 bloqueantes.** Opcional #1 **APLICADO** — `wireResponseFormat`/`wireJSONSchema` tipados (structs locales no exportados con `omitempty` en `Strict`), dando type-safety de compilador (un typo de clave ya no compila) y resolviendo D2 sin `if` explícito. Opcionales #2 (guard local de `schema` ausente — el upstream propaga 4xx, funcionalmente correcto, P7) y #3 (POST-AUDIT dentro de función "Rechazos" — cosmético) **DESCARTADOS con motivo**. Auditoría test↔postcondición: P1-P8 todos con test, ninguno sobrante, cero dependencia interna (AP-14).
- **Pipeline compartida sin regresión (P6):** `response_format` viaja como campo passthrough en `chatBodyMap` (inyectado antes del `Marshal`), preservado por clamp e inyección de thinking; atraviesa limiter/cache/clamp/ventana/budget/ruteo idéntico a 001. Cache exact-match con key separada por endpoint (P12 de 001): misma `schema` + mismo body → HIT; `schema` distinta → MISS (verificado por `TestE2E011002_P6_CachePorSchema`).

### Deuda técnica detectada

- **Ninguna deuda bloqueante.** Decisiones del brainstorm D1-D5 y fix #1 aplicados en su totalidad; #2/#3 de la review descartados con motivo documentado en `review.md`. La verificación empírica del soporte real de `response_format` por un provider (P7, desviación no bloqueante del spec) sigue pendiente — el diseño falla explícito con 4xx no-reintentable, sin degradación silenciosa.
- **Nota de higiene de state file:** `.cdad-state.json` tenía la clave `postconditions_status` **duplicada** (JSON ambiguo donde el segundo `map` sobreescribe al primero). Deduplicada en el cierre de esta feature.
- **Riesgo del epic vigente (plan-011, sin cambio):** Odoo 19 usa el Responses API en estado "preview"; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py` real).

### Próxima feature en cola

- **011-003-tool-calling** (epic 011-mofgw-odoo): habilita el ciclo tool-calling de Odoo. Reemplaza el rechazo P7 de 001 (400 "tool calling not yet supported"). El `call_id`/item id estable de 001 (P11) ya está pensado para este ciclo (Odoo reutiliza el id de item). Quedan 003-008 pendientes (001 y 002 done como troncos del epic; 009 staging-enterprise ya DONE). Coordinar vía `cdad-epic`.

### 12 Ago 2026 — Feature: 011-001-responses-endpoint (tronco del epic 011-mofgw-odoo)

### Decisiones relevantes

- **Feature 011-001-responses-endpoint DONE — `/v1/responses` en mofgw, tronco del epic 011-mofgw-odoo (reemplazo total de OpenAI en Odoo 19).** Implementa el Responses API (`POST /v1/responses`) que Odoo 19 enterprise usa en `_request_llm_openai_helper`, traduciendo Responses↔ChatCompletions no-stream. Se registra junto a `/v1/chat/completions` (proxy.go:283) bajo `s.auth.Wrap`. Suite completa **413 tests `-race`** verde en 17 paquetes (388 → 413, +25 tests feature), go vet limpio. Commits: `d2142f6` RED (24 fallan por 404 + 1 pasa 401 vía auth.Wrap), `3bec0b5` GREEN, `595e742` fixes review. Es el 1er feature del epic 011.
- **Traducción vive SOLO en la capa del endpoint (I1) — decisión arquitectónica del epic → ADR-004:** `handleResponses` (responses.go) traduce `input`→`messages`, delega en la MISMA pipeline de chat (`router.Complete`) y traduce la salida a `output[]`. `ParseChatRequest`, router y providers intactos. El body Responses viaja crudo por la cadena (pattern mofgw de transparencia). Esta es la frontera que las features 002-005 van a extender.
- **Modo solo NO-stream (D1) + rechazos explícitos de features futuras (P6-P9):** `stream:true` → 400 "streaming not supported yet"; `tools`/`web_search_preview` → 400 "tool calling not yet supported"; `text.format.json_schema` → 400 "structured output not yet supported"; part `input_file`/`input_image` → 400 "file attachments not yet supported". Features 002-005 reemplazan los rechazos; streaming SSE queda como deuda del epic.
- **`output[]` alineado al parser real de Odoo (D2, P10):** item `{"type":"message","id":"<stable>","role":"assistant","status":"completed","content":[{"type":"output_text","text":...}]}` — parseable por `_request_llm_openai_helper` (lee `content[i]["text"]`). Verificado contra `llm_api_service.py:364-397`.
- **IDs estables/determinísticas (P11):** sha256(clientID+body) con prefijo `resp_` → mismo input = mismo id de response y de item. Habilita que Odoo reutilice el id de item como `call_id` en el ciclo tool-calling de 003.
- **Cache separada por endpoint (P12):** `responsesCacheKey` usa namespace propio (prefix `"responses"` + clientID + body canónico) → una responses-response jamás se sirve como chat-response ni a la inversa. Cache solo para requests determinísticos (`temperature == 0`). La separación cross-endpoint es invariante estructural garantizada por el body wire (input vs messages).
- **Pipeline compartida con chat (P4):** limiter global + keyed + **por agente**, telemetría de descubrimiento (009-000), contextAnalysis (009-001), clamp por modelo, context-window check, budget, usage/cost accounting, `recordCacheTokens`, `setUsageHeaders`, `emitRequestTelemetry` + `emitRequestEnd`. `store` ignorado/stateless (P13); `max_output_tokens` no-op (Odoo no lo manda). Modelo forzado a nivel raíz con `rewriteResponseModel` (P5).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 1 bloqueante (test G P12 no ejercitaba la cache: bodies sin `temperature` → `isDeterministic=false` → MISS obligatorios y test vacío) **RESUELTO** agregando `temperature: 0` para ejercitar el camino HIT/MISS real. Opcionales #2 (telemetría/context/emitRequestEnd) y #3 (rate limit por agente) **APLICADOS**; #4 (singleflight) → deuda documentada, #5 (doble marshaling) y #7 (validación de role) descartados nits, #6 (model del envelope) no aplicado (FYI, patrón consistente con chat). Cero bloqueantes pendientes.

### Deuda técnica detectada

- **Llevadas a TECHDEBT consolidado:** streaming SSE del Responses API (fuera de alcance de 001, deuda del epic), singleflight/coalescing para `/v1/responses` (opcional #4 desestimado → feature futura), y el resto del scope cross-endpoint de 002-005 (structured output, tool calling, file attachments, web search) que hoy son 400.
- **Nota P12 (de la review):** la separación de namespace por endpoint es un invariante estructural garantizado por el hash del body wire responses (nunca coincide con el body wire chat), no end-to-end demostrable por el test.
- **Riesgo del epic vigente (plan-011):** Odoo 19 usa Responses API en estado "preview" para varias features; el módulo Odoo (011-008) debe mantenerse alineado a lo que Odoo manda/espera (mitigado con tests contra `llm_api_service.py` real).

### Próxima feature en cola

- **011-002-structured-output** (epic 011-mofgw-odoo): `text.format.json_schema` → `response_format` (AI Field Fill). Reemplaza el rechazo P8 (400 "structured output not yet supported"). Depende de 011-001. Coordinar vía `cdad-epic`. Quedan 002-008 pendientes (001 es el tronco; 009 staging-enterprise ya DONE).

### 11 Ago 2026 — fix: follower de single-flight factura su clientID + X-Usage-*

- **Bug corregido (RED→GREEN granular, `6ace500` + `d4727bd`):** en el coalescing single-flight (010-001 P0), `recordCacheTokens` y `setUsageHeaders` corrían solo dentro de `fn()` (el líder). El follower reutilizaba el blob compartido pero no facturaba a SU `clientID` (006-001 P2) ni exponía `X-Usage-*` en SU response (007-003 P1) — sus headers quedaban en cero. Encontrado por auditoría de Claude en `proxy.go`, sin test que lo cubriera.
- **Fix:** flag `leader` capturado en `fn()`; en la rama `shared==true`, para `!leader` se reconstruye `*provider.Usage` desde `sfRes.Usage` y se llama `recordCacheTokens(clientID)` + `setUsageHeaders(w.Header())` + `lastUsage`. El flag evita doble-facturar al líder. Nota menor: `singleflight.Usage` no lleva `ReasoningTokens` → el follower reporta reasoning 0 en `recordCacheTokens` (no afecta headers ni facturación por cliente; mejora separada si se quiere tracking exacto).
- **Test nuevo `TestSF_FollowerFacturaYExponeUsage` (e2e_singleflight_test.go):** bloquea el upstream del líder para garantizar la superposición y aserta que el follower (a) fue deduplicado (`mofgw_single_flight_deduped_total{client="client-k2"} 1` — condición de validez), (b) factura a su clientID en /metrics, y (c) recibe `X-Usage-Total-Tokens: 1280`.
- **Evidencia:** suite completa **388 tests `-race` verde en 17 paquetes** (387 → 388), build + vet + gofmt limpios. Bug era latente: `singleFlightEnabled` off por default — corregido de todos modos.

### 11 Ago 2026 — EPIC-010 mofgw-catalogo-fiel CERRADO + DEPLOYADO

### Decisiones relevantes

- **EPIC-010 (mofgw-catalogo-fiel) CERRADO — 2/2 features done, ambas mergeadas + desplegadas en prod + verificadas 11 Ago.** El epic cerró los huecos de fidelidad del catálogo detectados en la auditoría del 10 Ago: /v1/models ahora informa completo, veraz y prescriptivo (010-001), y el request path hace valer lo que el catálogo declara (010-002). Cadena entregada: catálogo fiel → clamp/inyección en el request path. Los 4 criterios de aceptación del epic (plan.md) verificados. Suite completa **387 tests `-race`** verde (332 → 387), vet + gofmt limpios, 17 paquetes.
- **010-001-catalogo-fiel (MERGED, deployado):** `/v1/models` emite por modelo — `supported_parameters` [tools,reasoning], `modality` + `architecture` (input/output_modalities; `text+image+video->text` para minimax/kimi/qwen, `text->text` para deepseek/glm/flash-0731), `top_provider` {context_length, max_completion_tokens}, `max_output_tokens`, y `thinking_default` **prescriptivo** (ADR-003). Metadata de `deepseek-v4-flash-0731` agregada. `qwen3.7-plus max_output` corregido 65536 → **131072** (D8). Fallback rule: capability no declarada/verificada se OMITE, nunca se adivina (ausente ≠ 0/[]/false).
- **C3 enmendado (P6 wins, 11 Ago):** la review del 010-001 detectó contradicción interna C3-vs-P6 — `top_provider`/`max_output_tokens` NO son capabilities nuevas sujetas a fallback rule, son **alias derivados** de `context_window`/`max_output` (007-002) y se emiten cuando sus fuentes son > 0. Enmendado en spec por decisión del dueño (GVR), tests respetan la enmienda.
- **010-002-request-path-fiel (MERGED, deployado):** (1) **clamp por modelo pre-routing** en el proxy (`min(raw, modelMaxOutput)`, convive con el clamp por provider del router → efectivo `min(modelo, provider)`) — kimi-k2.7-code (max_output 32768) con max_tokens 384000 ya no produce el 400→502; (2) **window-check fiel**: usa el max_tokens EFECTIVO post-clamp (`promptTokens + min(raw, MaxOutput)` vs `window×(1+margin)`), preservando el fix TECHDEBT #23; (3) **inyección per-attempt provider-aware** del `thinking_default` via knob declarativo `providers[].thinking_path` (`""`|`zen`|`bailian`, default `""` = sin inyección): zen → `reasoning_effort`; bailian → `reasoning_effort` + `enable_thinking` (deepseek/glm) o `enable_thinking` (qwen); kimi/minimax **NUNCA** (always-on / adaptive nativo == prescriptivo). Nunca pisa effort explícito del cliente (P5). Ruteo/fallback/clasificación intactos (4xx sigue no-reintentable, P9).
- **POST-AUDIT mandatorio aplicado:** `TestRED_MaxTokensCuentaEnVentana` (e2e_008001) cambió de contrato con la regla fiel — **flip 400 → 200** (750 + min(400,100) = 850 ≤ 1000). El test existente se actualizó ANTES del GREEN (premisa reemplazada: el max_tokens crudo ya no cuenta contra la ventana). Colisión de nombre de archivo resuelta: los tests de esta feature viven en `e2e_010002_requestpath_test.go` (el `e2e_010002_test.go` ya estaba ocupado por la feature response-cache, numeración previa del EPIC-010 de eficiencia).
- **Decisiones de arquitectura:** ADR-003 (thinking_default **prescriptivo** — el configurado altera el nativo) creado en 010-001. Knob `providers[].thinking_path` adoptado por decisión del dueño 11 Ago (declarativo, consistente con I2 — valores de config, nunca inferidos por patrón de ID: los IDs reales acct1/acct2/qwen son frágiles para inferir path).
- **Reviews (qwen3.7-plus, familia distinta al implementer):** 010-001 APPROVE WITH COMMENTS — bloqueante C7 (config.example.yaml) RESUELTO 11 Ago, 2 nits/FYI sin acción; 010-002 APPROVE WITH COMMENTS — 0 bloqueantes, Major #1 (qwen thinking) resuelto, Minor #2 → TECHDEBT #25, Minor #3 (thinking_path) resuelto, nits #4/#5 aceptados, FYI #6.
- **Verificado en prod (11 Ago):** `/v1/models` responde los 7 modelos con metadata completa; **bailian ACEPTA `max_tokens=131072` para qwen3.7-plus** (200, sin 400 — verificación empírica pendiente del spec resuelta); kimi `max_tokens=100000` → **200** (clamp funciona, sin 400/502); deepseek smoke → 200 con thinking activo (inyección). healthz 6 providers OK. Suite 387 tests `-race` verde, vet + gofmt limpios.

### Deuda técnica detectada

- **Llevadas al índice consolidado (TECHDEBT, Cambios 11 Ago):** #25 (`thinkingProfile` frágil ante cambios de config — derivación por niveles de Thinking orden-dependiente, ACEPTADA sin cambio estructural, LOW; si el catálogo evoluciona → campo declarativo `ThinkingFamily` o unit test que fije la semántica), #26 (`qwen3.7-plus max_output` 64K→128K corregido y verificado empíricamente en deploy: bailian acepta 131072), #27 (`bodyHasExplicitEffort` re-parsea el body — optimización opcional, LOW). #4 cerrada completa (flash-0731 pricing + metadata en prod + example).
- **Verificaciones empíricas pendientes (Phase 4, fuera del contrato):** glm-5.2 `reasoning_effort=medium` vía bailian (la tabla §5.4 dice high/max only — si bailian lo ignora silenciosamente, el wire value enviado es el configurado; discrepancia → TECHDEBT); context window real minimax/glm/qwen (1,000,000 vs 1,048,576 — verificación empírica del techo pendiente, NO cambiar valores). Nits de la review 010-001 sin registro (nit preexistente: `created: time.Now()` por respuesta; FYI: `top_provider` es eco del formato OpenRouter, sin concepto en mofgw).
- **Dos decisiones del 010-001 a mantener en memoria:** fallback rule (capabilities no verificadas se omiten) y modality honesta negativa (`text->text` = "no documentada como soportada", no conjetura).

### Próxima feature en cola

- **Ninguna — el epic 010 está cerrado.** El programa mofgw queda con los **10 epics done (001-010)**. Restan los criterios de aceptación del programa completo (plan.md §Criterios del programa): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes. Operativo residual: vigilar el anuncio de suba de precios de deepseek (TECHDEBT #4), monitorear cuota GO_2/GO_3 (#5), y la aceptación empírica de glm medium vía bailian cuando aplique.

### 09 Ago 2026 — Decisión: sticky_routing NO se habilita (póliza default-off)

- **Decisión formal del dueño del proceso: `fallback.sticky_routing` queda DESHABILITADO en prod (default off), documentado como decisión, no como deuda.** La feature 009-002 está implementada, testeada (332 tests) y mergeada — el costo de habilitarla es solo un toggle de config + reinicio.
- **Por qué no:** la data real muestra que no haría diferencia hoy — `failovers=0` y `cooldown_hits=0` en ~19.7K requests (la cadena nunca rotó), y el cache hit ya está en ~97.7% sin sticky (el orden de config ya es de facto sticky). El sticky solo paga cuando la cadena rota (cooldown/429/5xx), escenario no observado. Para openclaw/zot (sin sesión) es redundante con el orden de config.
- **Cuándo re-evaluar:** si aparecen cooldowns recurrentes (señal existente: TECHDEBT #5 — GO_2/GO_3 en 429 por cuota mensual). Si se habilita, validar empíricamente con la frecuencia del log `sticky_applied` en mofgw.log (esperado ~0 hoy).
- **Estado:** póliza activa — código listo, default off, ADR-002 documenta el diseño (reordenamiento post-filtro, nunca fuerza cooldown/health/cadena).

### 09 Ago 2026 — Verificación de criterios del programa en prod

- **Métricas vivas confirman el proxy absorbiendo todo el tráfico real:** 18,221 requests / 18,121 streams, **0 failovers, 0 cooldown hits, 87 errores totales** (todos absorbidos — el cliente nunca los ve). RAM 18.6M. Cache hit ~97.7% (2.31B hit vs 55M miss). Costos contabilizados: $77.09 USD total, cliente `zot`, por provider/modelo (`/metrics`).
- **Criterios del programa verificados en prod (plan.md §Criterios):** /metrics contabiliza tokens y costo por cliente/modelo/provider ✅; /v1/models responde (con capabilities, 007-002) ✅; rechazo temprano por ventana (008-001) activo en el binario ✅; omniroute no aparece como servicio ✅ (criterio E2E integral 48h + 10+ agentes = validación de observación pendiente).
- **Telemetría viva (1,943 eventos):** opencode capturado con `X-Session-Id`/`X-Session-Affinity` reales, key paths correctos, sin contenido (privacidad). 
- **Composición habilitada:** `/v1/context` responde 401 sin auth (ruta registrada + protegida — esperado); la verificación autenticada de acumulación de records requiere la key plana del cliente `zot` (operativo del operador, no expuesto en el registro).

### 09 Ago 2026 — Deploy del epic 009 en producción

- **Binario con las 3 features del epic desplegado** (09 Ago, ~02:12 ART): el binario anterior en `~/.local/bin/mofgw` NO tenía 009-001/009-002 (verificado por strings: 0 matches vs 16 del build nuevo). Reemplazado por build fresco (`go build ./cmd/mofgw`, 10.3M), backup en `mofgw.bak-20260809`. Servicio reiniciado: healthz OK (6 providers healthy), state.json restaurado (v1→v2 sin error — C12 funcionando en prod), config cargada, tráfico real fluyendo (WARN "telemetry header negado" de agentes con X-Parent-Session-Id — telemetría 009-000 viva).
- **`context.analysis` HABILITADO en prod** (read-only, no invasivo — P8 de 009-001): `~/.config/mofgw/config.yaml` → `context: {margin: 0.1, analysis: {enabled: true, history_per_session: 50}}`. El endpoint `GET /v1/context` queda activo para el cliente `zot` (auth Bearer).
- **`fallback.sticky_routing` queda DEFAULT OFF** (decisión de criterio): es la feature más delicada para activar en tráfico real (cambia ruteo de requests vivos). Se habilita tras observar la composición funcionando — el diseño post-filtro (ADR-002) garantiza que no rompe la cadena si se activa, pero se prefiere activación escalonada.
- **plan.md actualizado** (commit `3a484f8`): status del cierre del epic 009 + criterios del programa 9/9 done.

## 2026-08-09 — Epic 009 cerrado

### Decisiones relevantes

- **EPIC-009 (mofgw-contexto-analisis) CERRADO — 3/3 features done, integración cross-feature verde (E3) y closure (E4) completados.** El epic cumplió su objetivo: descubrir y optimizar la composición del contexto del tráfico real. Cadena entregada: telemetría de descubrimiento (009-000, desplegada en prod 09 Ago) → composición de contexto `/v1/context` (009-001) → sticky routing por sesión (009-002). Los 4 criterios de aceptación del epic (plan.md:211-215) verificados: E2E cross-feature `TestE2EEpic009_FlujoCompuesto` (6 subtests) verde; suite completa **332 tests `-race`** (326 + 6 cross-feature); **cero bugs cross-feature** (integration-009.md, commit `da8446c`).
- **Dual-keying confirmado por captura real (~103 eventos) — el criterio de cierre del epic (ADR-001, creado en 009-001):** solo opencode manda `X-Session-Id` (header); openclaw/zot no (ni header ni metadata, `detected_ids` = 0 en todos los eventos) → composición y sticky keyed `client|session` para opencode y `client|` para openclaw/zot, conviviendo simultáneamente en runtime.
- **Sticky como reordenamiento post-filtro (ADR-002, creado en 009-002):** `applyStickyReorder` solo mueve al preferido al frente del slice `ready` post-`resolveReady` (cooldown/health/Serves ya aplicaron); nunca fuerza. El criterio del epic "respeta cooldown/health/cadena" (plan.md:215) queda cubierto por diseño, no por accidente.
- **Integración E3 sin fricción:** las 3 features conviven en el proxy sin pisarse — la fase 2 de 009-001 (`UpdateContextUsage`) y el registro sticky de 009-002 (`Affinity().Set`) viven en el MISMO punto (`recordCacheTokens`) y coexisten sin conflicto; el dual-keying se confirmó en el flujo real con y sin sesión. Cero refactor de features done.
- **Operativo pendiente (default off — no altera comportamiento sin habilitarlo):** activar `context.analysis.enabled: true` (009-001) + `fallback.sticky_routing.enabled: true` (009-002) en `~/.config/mofgw/config.yaml` de prod. `telemetry` ya está activa (009-000, desplegada).

### Deuda técnica detectada

- **El epic se lleva al índice consolidado (TECHDEBT):** #20 (gofmt preexistente en `e2e_009000_test.go`, LOW), #21 (concurrencia sticky sin test dedicado — hardening post-cierre, LOW), #22 (`X-Session-Affinity` sin test dedicado — hardening post-cierre, FYI) + nits sin registro de la review 009-002 (idiom `evictLRU`; `AffinityStore` incondicional — aceptados, sin acción). Ninguna bloqueante; el resto de la deuda del epic (16-19) ya está registrada.
- **Dos lecciones de proceso del epic (AUDIT post-hoc, documentadas en TECHDEBT §Deuda de proceso):** (1) 009-001 — el AUDIT previo al RED debe escanear literales de fixture que el spec cambia (bump `stateVersion` 1→2); (2) 009-002 — los contadores/harness deben medir lo que el test afirma (5 de 32 tests nuevos nacieron con bugs de test, detectados en GREEN).

### Próxima feature en cola

- **Ninguna — el epic 009 está cerrado.** El programa mofgw queda con los 9 epics done (001-009). Siguiente paso: criterios de aceptación del programa completo (plan.md §Criterios del programa) — E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes. Operativo opcional: habilitar `context.analysis` + `sticky_routing` en prod.

### 09 Ago 2026 — Feature: 009-002-sticky-session (cierre de ciclo — última del epic)

## 2026-08-09 — Feature: 009-002-sticky-session

### Decisiones relevantes

- **Afinidad como reordenamiento post-filtro (I2/P9) — el corazón de la feature:** `applyStickyReorder` actúa SOLO sobre el slice `ready` post-`resolveReady` (ya post-cooldown/health/Serves): si el preferido está en `ready`, se mueve al frente preservando el orden relativo del resto; si NO está (cooldown, unhealthy, ya no sirve el modelo, clave ausente/evictada, stickyKey vacío) → slice intacto, comportamiento IDÉNTICO a legacy. NUNCA fuerza un provider que el filtro descartó, nunca cambia `maxAttempts` ni la clasificación de errores. Garantiza el criterio del epic (plan.md:215, "respeta cooldown/health/cadena") y la transparencia total (P7/I1/D6): el cliente nunca percibe el ruteo; única diferencia observable es el log debug `sticky_applied`. → **ADR-002**.
- **Keying `client|session` + `client|` (P2, consume ADR-001):** misma fuente de sesión que 009-001 — `X-Session-Id` solo lectura, nunca upstream (I4). opencode → `clientID|sessionID` (afinidad por sesión real); openclaw/zot sin sesión → `clientID|` (afinidad por cliente, comportamiento deseado según captura 009-000). `X-Session-Affinity` deliberadamente NO se usa (D5/I3: en opencode es idéntico a `X-Session-Id`). `clientID` nunca vacío (token autenticado) → clave nunca `""`.
- **`AffinityStore` efímero LRU (P3/D3/D4):** store en memoria (patrón `CooldownStore`; restart → frío; sin bump de `stateVersion`), evicción LRU por last-used (Set y Get refrescan — un cliente activo no se evicta, C12), cap = `Options.StickyMaxEntries` REUTILIZANDO el MISMO knob `server.max_sessions_retained` (default 100): D1 limita la config a `enabled`, el operador gobierna retención de sesiones y afinidad con un solo knob. Claves `client|` cuentan contra el mismo tope; pérdida benigna (re-registra en frío).
- **`CompleteFor`/`StreamFor` con backward compat total (P8/D2):** refactor del loop de `Complete`/`Stream` a helpers compartidos (`complete`/`stream` + `applyStickyReorder`); `Complete`/`Stream`/`New` legacy intactos (stickyKey `""` → retorno inmediato); `StickyMaxEntries` nuevo en Options (0 = default). Con sticky off (default) el proxy llama legacy → cero diferencias observables (C11), e2e existentes pasan sin cambios.
- **Registro post-éxito del GANADOR (P6):** en `recordCacheTokens` (punto único post-éxito con providerID para stream y no-stream), gated por `s.stickyRouting` (off → store vacío, cero memoria). Registra el provider que RESPONDIÓ/arrancó el stream (no necesariamente el preferido). Fallo total (502) o rechazos pre-router (400/413/limiter/budget/ventana) NO tocan la afinidad (C9); stream arrancado que muere a mitad SÍ registra (cache calentado). Best-effort: error de registro jamás falla el request.
- **No invasivo + bounds (P9):** sin cambios en limiter/budget/ventana/clamp/usage/persistencia (`stateVersion` intacta)/telemetría; sin métricas nuevas (solo log debug); cardinalidad acotada (LRU) + mutex, seguro concurrente. Limitación aceptada (D4): afinidad por clave, no por `(clave, modelo)` — el óptimo de cache del segundo modelo puede no alcanzarse; corrección siempre garantizada vía `Serves`.
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes, 5 opcionales (4 aceptados como deuda #21-#22 + nits sin registro; 1 aplicado — Nit #5: `max_sessions_retained` documentado en `config.example.yaml`). Suite completa 326 tests `-race` verde (294 + 32 nuevos), vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Feature aditiva (test-audit):** 0 tests existentes modificados, 38 archivos untouched, 32 tests nuevos (3 config + 18 router + 11 e2e) que cubren P1-P9 / C1-C13; 5 fixes de tests NUEVOS documentados (§5.2) con lección: **los contadores deben medir lo que el test afirma** (`fakeProvider.Stream` no cuenta en `callCount`; el health check de `buildSticky` contamina contadores; `upstreamOK` no produce costo → budget nunca dispara).
- **Operativo:** la feature cierra el ciclo (merge) y el EPIC-009 (3/3 done). Habilitar `fallback.sticky_routing.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo) — junto con `context.analysis.enabled` de 009-001.

### Deuda técnica detectada

- **TECHDEBT #21** — concurrencia sticky sin test dedicado (C13): garantía estructural (mutex de `AffinityStore`) + `-race` en suite; sin TOCTOU. Test concurrente dedicado → hardening post-cierre.
- **TECHDEBT #22** — `X-Session-Affinity` irrelevante sin test dedicado (I3/D5): garantía por construcción (proxy solo lee `X-Session-Id`) + guard `e2e_008003` untouched. Test dedicado → hardening post-cierre.
- **Nits de la review sin registro** (aceptados, sin acción de mejora): `evictLRU` con flag `first` (idiom Go correcto, refactor cosmético opcional); `AffinityStore` creado incondicionalmente (costo ~120 bytes, consistente con `CooldownStore`); `Get` refresca siempre `LastUsed` (semántica EXACTA del spec P3, revisado "no cambiar").
- **Lección de proceso (test-audit §2/§5.2):** AUDIT previo al RED no ejecutado como sesión formal → 5 de los 32 tests nuevos nacieron con bugs de test (detectados en GREEN: 3 por el implementer respetando AP-4, 2 por el orquestador). Lección: además de escanear fixtures (009-001), escanear que los contadores/harness midan lo que el test afirma medir. Ver entrada de proceso en TECHDEBT.

### Próxima feature en cola

- **Ninguna del epic 009 — las 3 features están done.** Siguiente paso: **integración cross-feature (E3) y closure del epic (E4)** vía `cdad-epic` (chat nuevo): verificar los criterios de aceptación del epic (telemetry.jsonl capturando; `docs/research-context-patterns.md`; `/v1/context?session=<id>`; sticky routing opcional por config transparente que respeta cooldown/health/cadena — este último cubierto por 009-002), habilitar las features en prod y decidir el cierre formal.

### 09 Ago 2026 — Feature: 009-001-context-composition (cierre de ciclo)

## 2026-08-09 — Feature: 009-001-context-composition

### Decisiones relevantes

- **Dual-keying confirmado por captura (~103 eventos) — decisión de cierre del epic resuelta:** opencode manda `X-Session-Id` en header → composición keyed `client|session`; openclaw/zot NO mandan sesión (ni header ni metadata del body, `detected_ids` = 0 en todos los eventos) → composición keyed `client|` (agregado por client_id, fallback diseñado). Ambos keyings conviven simultáneamente en runtime (P2/C15). Fuente: `docs/research-context-patterns.md`. → **ADR-001**.
- **Endpoint `GET /v1/context` con aislamiento por clientID** (auth Bearer sobre `/v1/*`): `?session=<id>` → `scope:"session"` con 404 si la sesión no existe para el cliente (consistente 008-003 P5); sin session → `scope:"client"` con el agregado `client|` (incluye TODO el tráfico del cliente, con y sin sesión). Shape `{client, scope, session?, requests, summary, latest, history}`, ceros/vacío nunca nil (P3).
- **Parser estructural en UN solo recorrido (`composition.Analyze`):** superset de `Walk` (absorbe el refactor del audit 009-000 R1); `Walk` intacto y verificado semánticamente idéntico (C3, schema lock R10 de telemetría verde); privacidad por construcción — solo metadata estructural, nunca contenido (P7/I1).
- **`prompt_tokens_actual` en DOS fases:** fase 1 pre-request (mismo gate que telemetría: body válido, stream y no-stream, incluidos los rechazos por limiter/budget/ventana/upstream — P4/C9); fase 2 post-response con matcheo exacto por `request_id` (evita races entre handlers concurrentes de la misma sesión). Best-effort: error de análisis jamás falla el request (I7).
- **stateVersion 1→2 con backward compat (P5/I6):** `persistedState` gana `Contexts` (`omitempty`, nil-safe para v1); binario nuevo lee snapshot v1 sin error; binario viejo rechaza v2 con el check existente (persist.go:243); round-trip preserva contexts con history.
- **Config `context.analysis` aditiva (P6):** `enabled=false` default (sin records ni memoria; endpoint 200 con ceros/vacío), `history_per_session=50` (0 = sin history, solo agregados), valida `< 0`; INDEPENDIENTE de `telemetry`. Wiring pre-tráfico `srv.SetContextAnalysis(...)` + `m.SetContextHistoryPerSession(...)` (patrón SetContextMargin/SetMaxSessionsRetained, main.go:217-218).
- **Review APPROVE (qwen3.7-plus, familia distinta al implementer):** 0 bloqueantes (1 reportado desestimado con motivo escrito — nil-slices falso positivo: `make(..., 0, len(...))` nunca emite `null`, `[]` garantizado), 5 opcionales aceptados como deuda documentada (#16-#20). Suite completa 294 tests `-race` verde, vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente #20).
- **Operativo:** la feature cierra el ciclo (merge). Habilitar `context.analysis.enabled: true` en config de prod es paso operativo pendiente (default off; no altera comportamiento sin activarlo).

### Deuda técnica detectada

- **TECHDEBT #16** — redundancia `SetContextHistoryPerSession` en main.go:218 (defensa en profundidad; limpiar en refactor futuro).
- **TECHDEBT #17** — casing PascalCase de `PartType` a nivel entry del endpoint (vs snake_case del summary; consumidores propios no dependen).
- **TECHDEBT #18** — `latest: null` vs "ausente" con `history_per_session=0` (cosmética; representación JSON válida, e2e test la espera).
- **TECHDEBT #19** — ring buffer con prepend O(N) en context.go:171 (FYI de hardening; default 50 → despreciable; deque circular O(1) futuro).
- **TECHDEBT #20** — gofmt preexistente en `e2e_009000_test.go` (preexistente en HEAD, fuera del diff de esta feature; correr gofmt cuando se toque el archivo).
- **Lección de proceso (test-audit §2/§9.1):** AUDIT previo al RED no ejecutado como sesión formal → `TestPersistVersionReject` falló en GREEN porque el bump P5 cambia un literal de fixture (`"version":1` → `"version":2`) y el replace era no-op. Corregido en sesión B (6e28730) preservando la intención. Lección: el AUDIT de una feature con bump de schema debe escanear literales de fixture que el spec cambia, no solo call-sites de firma.
- (menor, test-audit §9.3) acceso a campo interno en `TestPersistContextV2_BackwardCompatV1` (persist_context_test.go:39-44) — alternativa observable (`ContextSnapshot(client, "")`) recomendada para hardening futuro.

### Próxima feature en cola

- **009-002-sticky-session** (pendiente del epic mofgw-009): afinidad de provider por sesión para maximizar cache hit. Consume la composición de 009-001 y la fuente de sesión confirmada (`X-Session-Id` de opencode; client_id para openclaw/zot). Coordinar vía `cdad-epic` (chat nuevo) — el orquestador sugiere volver al epic al cerrar esta feature.

### 09 Ago 2026
- **Feature 009-000-request-telemetry DESPLEGADA en producción:** telemetría de requests activa (`telemetry.enabled: true, sample_rate: 1`), archivo `/home/<user>/logs/mofgw-telemetry.jsonl` (0640). Ciclo CDAD completo: spec → audit → RED → GREEN → review (APPROVE qwen3.7-plus, 0 bloqueantes). Suite 265 tests -race verde.
- **Hallazgo de infraestructura (causa raíz artifacts vacíos):** `fallback.timeout: 120s` cortaba streams largos de sub-agentes (el timeout aplica a todo el intento, no solo TTFB — discrepancia contrato-vs-impl). Fix en prod: 120s→300s. Documentado en `docs/hallazgos/2026-08-09-timeout-streams-largos.md`. Fix de diseño (TTFB-only vs stream_timeout) pendiente en backlog.
- **Descubrimiento en curso (24-48h):** telemetría capturando tráfico real. Primeros datos confirman la investigación — opencode manda X-Session-Id en header; el runtime OpenAI/JS (zot/openclaw) NO manda sesión ni en headers ni en metadata del body.

### 08 Ago 2026
- **Cierre formal del replanteo de eficiencia:** P4 CERRADO — Memory Bank aprobada por Pablo, `.cdad-state.json` marcado epic-closed (006/007/008 done). Validación externa CERRADA (7 APPROVE + 2 REQUEST CHANGES corregidos).
- **EPIC-009 planificado y aprobado:** telemetría de tráfico (009-000) → composición de contexto (009-001) + sticky routing por sesión (009-002). Fase 1 = telemetría: loguear headers/metadata del body del tráfico real para descubrir dónde viven los ids de sesión (investigación confirmó: solo opencode manda X-Session-Id; openclaw y zot no). Destino: telemetry.jsonl dedicado.

### 07 Ago 2026
- **Memory Bank creada** (cdad-scribe): `docs/projectbrief.md` + `docs/activeContext.md` — Etapa 5 del epic cycle completada.
- **GAP 1 resuelto:** `docs/specs/external-reviews/` ya existe con 9 `.resp.json` (evidencia de validación externa). El reporte del orchestrator era incorrecto.

### 06 Ago 2026
- **Replanteo de eficiencia completo:** EPIC-003/006/007/008 — 9 features, todas spec→audit→RED→GREEN→review.
- **SEC-001 security hardening:** 4 fixes de auditoría externa (SSE saneado, X-Agent-Id truncado+TTL, ReadHeaderTimeout, log 0640). Review con qwen3.7-plus (familia distinta).
- **Persistencia de accounting:** SaveState/LoadState JSON atómico (commit f90c0b7).
- **Validación externa:** 7 APPROVE + 2 REQUEST CHANGES. Ambos Majors corregidos (commit 082d10c).
- **Pricing/metadata reales** cableados en config de producción.

### 05 Ago 2026
- **EPIC-001 MVP completo** (commit e426f15d, 27 files, 3808 ins).
- **EPIC-002 completo** (commit 5966c5f4, 133 tests PASS).
- **EPIC-004/005 implementados y desplegados.**
- **Hotfix 001-001-endpoint-fix-content-array:** mofgw devolvía 400 a OpenClaw (content array vs string). Fix: `Messages` → `[]json.RawMessage`.
- **Feature 005-005-verbose** implementada (flags --verbose/--log-file, privacidad testeada).
- **Feature 001-003-fallback-v2** implementada (max_retries como tope de intentos totales, cooldown entre requests).

### 04 Ago 2026
- **Plan de epics aprobado** por Pablo (sesión Telegram) — con auth in-scope del MVP.
- **Todas las specs escritas** (EPIC-001 7/7, EPIC-002 4/4, EPIC-005 4/4).

### 03 Ago 2026
- **Decisiones fundacionales** (Pablo): nombre mofgw, transparencia total, puerto 3369, deploy systemd user, config paths.

## Próximos pasos

1. **EPIC-009 CERRADO** (3/3 features done + integración E3 + closure E4). Operativo opcional pendiente: habilitar `context.analysis.enabled` + `fallback.sticky_routing.enabled` en config de prod (default off).
2. **Criterios de aceptación del programa** (ver plan.md §criterios): E2E integral 48h con OpenClaw, omniroute deshabilitado, 10+ agentes concurrentes, /metrics con tokens/costo, /v1/models capabilities, rechazo temprano por ventana.
3. **Deuda técnica** (ver `docs/TECHDEBT.md`): 24 entradas registradas (#16-24 = epic 009 + decisiones), varias cerradas, resto BAJA/FUTURO.
4. **Budget para cliente zot/OpenClaw** (decisión de Pablo pendiente — evaluado y diferido 08 Ago: el gasto es intencional, no se limita).

## Conocimiento operativo clave

- **Config de producción:** `~/.config/mofgw/config.yaml` — providers reales, pricing Zen, context.margin 0.1.
- **State file:** `~/.config/mofgw/state.json` (0600) — contadores de accounting, persistidos cada 10min.
- **Logs:** `/home/<user>/clawd/projects/mofgw/logs/` (permisos 0640).
- **Service:** `systemctl --user status mofgw` — active + enabled.
- **RAM:** ~12MB en operación normal.
- **Cache hit rate:** 94-99% medido en /metrics.

## Archivos de referencia

| Archivo | Qué contiene |
|---------|-------------|
| `docs/projectbrief.md` | Identidad, decisiones irreversibles, epics |
| `docs/activeContext.md` | Este archivo — estado actual, decisiones recientes |
| `docs/epics/plan.md` | Plan completo de epics con decomposición |
| `docs/research-architecture.md` | Arquitectura recomendada (ReverseProxy + cooldown RAM) |
| `docs/research-token-efficiency.md` | Precios verificados, cache providers, thinking capabilities |
| `docs/research.md` | Competidores y decisiones iniciales |
| `docs/USER-GUIDE.md` | Guía de usuario (instalación, config, endpoints) |
| `docs/TECHDEBT.md` | Deuda técnica consolidada (24 entradas) |
| `docs/specs/` | Specs de cada feature (27 directorios) |
| `docs/specs/external-reviews/` | Evidencia de validación externa (9 .resp.json) |
| `docs/specs/CROSS-SPEC-REVIEW.md` | Revisión de consistencia entre specs EPIC-001 |
| `task_plan.md` | Plan de tareas con estado de cada feature |
| `.cdad-state.json` | Estado del ciclo CDAD |
