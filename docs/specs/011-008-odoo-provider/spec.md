# Spec — 011-008-odoo-provider: registro de mofgw como provider de IA de Odoo

---
feature_id: 011-008-odoo-provider
feature_name: odoo-provider
epic: 011-mofgw-odoo
status: draft
created_at: 2026-08-12
approved_by: pendiente
depends_on: 011-007-embedding-vector
---

## Descripción funcional

Registrar **mofgw** como provider de IA de Odoo 19 enterprise, de modo que una
instancia Odoo (módulo local `mofgw_ai`) pueda usar mofgw como gateway de LLM y
embeddings — reemplazo total de OpenAI (objetivo del epic 011). Odoo elige el
provider por el `llm_model` del agente y por el modelo de embeddings; mofgw
sirve `/v1/responses` y `/v1/embeddings` con formato OpenAI-compatible
(features 011-001 y 011-006), por lo que el provider se registra reutilizando
el flujo openai de `LLMApiService` (`_request_llm_openai`, formato de tool
call openai), apuntando `base_url` a mofgw y usando una API key propia.

**Mecanismo (D1 — monkeypatch in-place):** el core de `ai` instancia la clase
`LLMApiService` **por nombre** y comparte el objeto `PROVIDERS` por referencia.
Por eso no se subclasea ni se re-declara nada en el core: el módulo local hace:

1. `PROVIDERS.append(Provider('mofgw', ...))` en el import del módulo → lo ven
   `get_provider`, `get_provider_for_embedding_model` y
   `ai.agent._get_llm_model_selection()` (que leen `PROVIDERS` por referencia).
2. Parchear in-place los métodos de la clase base `LLMApiService`:
   `__init__` (caso `'mofgw'` → `base_url` de `ai.mofgw_url`),
   `_get_api_token` (caso `'mofgw'` → `ai.mofgw_key` / env `ODOO_AI_MOFGW_TOKEN`),
   `_request_llm` (caso `'mofgw'` → `self._request_llm_openai(...)`),
   `_build_tool_call_response` (caso `'mofgw'` → formato openai).
   Cada parche delega al método original para `openai`/`google` (sin regresión).

**Embedding model (D3 — override obligatorio):** `PROVIDERS.append` NO
actualiza `EMBEDDING_MODELS_SELECTION` (snapshot por comprensión calculado al
importar `llm_providers`, llm_providers.py:43-45) ni el Selection `ai.embedding.
embedding_model` ya registrado. El módulo ya hace `_inherit='ai.embedding'`
(por 011-007); se re-declara `embedding_model = fields.Selection(
selection_add=[('all-minilm', 'All-minilm')])`. El `embedding_model` del
Provider mofgw es `'all-minilm'` (384, alineado con 011-007), así
`ai.agent._get_embedding_model()` devuelve `'all-minilm'` para agentes mofgw y
el RAG/cron escriben vectores 384-dim consistentes con la columna `vector(384)`.

**Config por instancia (D4):** `ir.config_parameter` `ai.mofgw_url` +
`ai.mofgw_key`, con fallback a env `ODOO_AI_MOFGW_TOKEN`. `res.config.settings`
hereda fields `mofgw_url`/`mofgw_key` (config_parameter) + una view heredando
`res_config_settings_view_form` para la UX del operador. **URL default (D5):**
`http://127.0.0.1:3369/v1` (mofgw sirve `/v1/responses` y `/v1/embeddings`).

**Mismo módulo que 011-007:** no se tocan el override del vector ni el
pre_init_hook ya existentes; se agregan los archivos de esta feature al mismo
`mofgw_ai`. Comparten instalación (D4).

**Deuda documentada (D6):** el path separado `ir_actions_server.AI_PROVIDER`
(hardcodeado a `"openai"`/`"gpt-4.1"`, ir_actions_server.py:26-27) queda **fuera
de scope** de esta feature: el path principal de Odoo es `ai.agent` (que usa
`get_provider` + `LLMApiService(provider=...)` y por eso lo cubre este registro).
Hacer que las *server actions* `state='ai'` usen mofgw requiere tocar ese
atributo de clase y queda como deuda del epic, documentada en Out of scope.

## Contrato

### Firma — Provider mofgw (registro vía monkeypatch, D1)

```python
# en mofgw_ai/__init__.py (o util importada al cargar el módulo)
from odoo.addons.ai.utils.llm_providers import PROVIDERS, Provider

PROVIDERS.append(Provider(
    "mofgw",
    "mofgw",
    "all-minilm",
    [
        ("deepseek-v4-flash", "DeepSeek V4 Flash"),
        ("deepseek-v4-pro", "DeepSeek V4 Pro"),
        ("qwen3.7-plus", "Qwen 3.7 Plus"),
        ("glm-5.2", "GLM 5.2"),
        ("kimi-k2.7-code", "Kimi K2.7 Code"),
    ],
))
```

### Firma — Parches in-place de `LLMApiService` (D1)

Cada parche captura el método original como global del módulo y delega para los
providers que no son `'mofgw'` (sin regresión).

```python
from odoo.addons.ai.utils.llm_api_service import LLMApiService

_orig_init = LLMApiService.__init__
def _patched_init(self, env, provider='openai'):
    if provider == 'mofgw':
        self.provider = provider
        self.base_url = env["ir.config_parameter"].sudo().get_param(
            "ai.mofgw_url", "http://127.0.0.1:3369/v1")
        self.env = env
    else:
        _orig_init(self, env, provider=provider)
LLMApiService.__init__ = _patched_init

_orig_token = LLMApiService._get_api_token
def _patched_get_api_token(self):
    if self.provider == 'mofgw':
        cfg = {"config_key": "ai.mofgw_key", "env_var": "ODOO_AI_MOFGW_TOKEN"}
        if key := (self.env["ir.config_parameter"].sudo().get_param(cfg["config_key"])
                   or os.getenv(cfg["env_var"])):
            return key
        raise UserError(_("No API key set for provider 'mofgw'"))
    return _orig_token(self)
LLMApiService._get_api_token = _patched_get_api_token

_orig_llm = LLMApiService._request_llm
def _patched_request_llm(self, *args, **kwargs):
    if self.provider == 'mofgw':
        return self._request_llm_openai(*args, **kwargs)
    return _orig_llm(self, *args, **kwargs)
LLMApiService._request_llm = _patched_request_llm

_orig_build = LLMApiService._build_tool_call_response
def _patched_build_tool_call_response(self, tool_call_id, return_value):
    if self.provider == 'mofgw':
        return {"type": "function_call_output", "call_id": tool_call_id,
                "output": str(return_value)}
    return _orig_build(self, tool_call_id, return_value)
LLMApiService._build_tool_call_response = _patched_build_tool_call_response
```

### Firma — Override de `ai.embedding.embedding_model` (D3)

```python
# en mofgw_ai/models/ai_embedding.py (ya _inherit='ai.embedding' por 011-007)
from odoo import fields, models

class AIEmbedding(models.Model):
    _inherit = 'ai.embedding'
    embedding_vector = Vector(size=384)              # de 011-007, no tocar
    embedding_model = fields.Selection(
        selection_add=[('all-minilm', 'All-minilm')])  # agrega al Selection existente
```

### Firma — Config por instancia (D4)

```python
# en mofgw_ai/models/res_config_settings.py (nuevo)
class ResConfigSettings(models.TransientModel):
    _inherit = 'res.config.settings'
    mofgw_url = fields.Char(string="mofgw URL", config_parameter='ai.mofgw_url')
    mofgw_key = fields.Char(string="mofgw API key",
                            config_parameter='ai.mofgw_key')
```
Nota (revisión #2): el masking de la key se hace vía `widget="password"` en la
view (mismo patrón que el core `openai_key`/`google_key`, res_config_settings.py
del core NO usa `password=True` en el field). El field se declara sin
`password=True`, consistente con el core.

### Postcondiciones

**Registro del provider (D1)**

1. **P1 — Provider `'mofgw'` registrado en `PROVIDERS`:** con el módulo
   `mofgw_ai` cargado, `PROVIDERS` contiene un `Provider` con `name == 'mofgw'`,
   `display_name == 'mofgw'`, `embedding_model == 'all-minilm'` y `llms` con los
   5 modelos (deepseek-v4-flash, deepseek-v4-pro, qwen3.7-plus, glm-5.2,
   kimi-k2.7-code). `get_provider(env, 'deepseek-v4-flash') == 'mofgw'` y
   `get_provider_for_embedding_model(env, 'all-minilm') == 'mofgw'`.

2. **P2 — `_get_llm_model_selection()` expone los modelos mofgw:** el Selection
   del campo `ai.agent.llm_model` (ai_agent.py:262-266) incluye los 5 modelos de
   `llms` del provider mofgw (porque lee `PROVIDERS` por referencia).
   `env['ai.agent']._get_llm_model_selection()` contiene el tuple de cada modelo
   mofgw.

3. **P3 — `ai.agent._get_embedding_model()` devuelve `'all-minilm'` para mofgw:**
   para un agente con `llm_model` mofgw, `_get_embedding_model()` (ai_agent.py:
   370-376, itera `PROVIDERS`) retorna `'all-minilm'`. Así el RAG (`_build_rag_
   context`, ai_agent.py:594-599) y el cron de embeddings escriben vectores
   384-dim consistentes con la columna `vector(384)` de 011-007.

**Métodos parcheados de `LLMApiService` (D1)**

4. **P4 — `__init__` resuelve `base_url` para `'mofgw'`:** `LLMApiService(env,
   provider='mofgw').base_url == get_param('ai.mofgw_url',
   'http://127.0.0.1:3369/v1')`. Con `ai.mofgw_url` seteado usa ese valor; sin
   él usa el default D5. `openai`/`google` conservan su `base_url` original
   (sin regresión).

5. **P5 — `_get_api_token` para `'mofgw'`:** con `ai.mofgw_key` seteado devuelve
   ese valor; con config ausente y env `ODOO_AI_MOFGW_TOKEN` seteado devuelve el
   env var; con ninguno levanta `UserError` "No API key set for provider
   'mofgw'". `openai`/`google` conservan su token original (sin regresión).

6. **P6 — `_request_llm` para `'mofgw'` delega a `_request_llm_openai`:** con
   `provider='mofgw'`, `_request_llm(...)` invoca `_request_llm_openai(...)` (el
   request sale por `POST <base_url>/responses`). `openai`/`google` conservan su
   despacho original (sin regresión).

7. **P7 — `_build_tool_call_response` para `'mofgw'` produce formato openai:**
   `_build_tool_call_response(id, val)` con `provider='mofgw'` devuelve
   `{"type":"function_call_output","call_id":id,"output":str(val)}`. `openai`/
   `google` conservan su formato original (sin regresión).

**Embeddings (sin parche propio)**

8. **P8 — `get_embedding` de mofgw usa `base_url` + token parcheados:**
   `LLMApiService(env, provider='mofgw').get_embedding(input, dimensions=384,
   model='all-minilm')` sale por `POST <base_url>/embeddings` con header
   `Authorization: Bearer <ai.mofgw_key>` — porque `get_embedding` (llm_api_
   service.py:100-121) usa `self._request` + `_get_base_headers()` +
   `_get_api_token()`, ya parcheados (P4, P5). No requiere parche adicional.

**Config por instancia (D4)**

9. **P9 — Settings hereda `mofgw_url`/`mofgw_key`:** `res.config.settings`
   expone `mofgw_url` y `mofgw_key` con `config_parameter` `ai.mofgw_url` y
   `ai.mofgw_key`. Guardar `mofgw_url` en settings setea `ir.config_parameter`
   `ai.mofgw_url` (y análogo para la key). Existe una view heredada de
   `res_config_settings_view_form` que expone ambos fields.

**Override de `ai.embedding.embedding_model` (D3)**

10. **P10 — `ai.embedding.embedding_model` incluye `'all-minilm'`:** con el
    módulo cargado, `env['ai.embedding']._fields['embedding_model'].
    get_values(env)` (o `selection`) contiene `('all-minilm', 'All-minilm')`.
    El field conserva los valores del core (openai/google) y su `required=True`.

**Verificación funcional (end-to-end)**

11. **P11 — `request_llm` end-to-end con `'mofgw'`:** con `ai.mofgw_url`
    apuntando a una instancia mofgw real y key válida,
    `LLMApiService(env, provider='mofgw').request_llm('deepseek-v4-flash',
    [system], [user])` retorna una lista de respuestas no vacía; el request
    upstream va por `POST <ai.mofgw_url>/responses` con formato OpenAI. (E2E en
    staging — ver "Verificaciones pendientes".)

## Invariantes verificables

- **I1 — El core `ai` enterprise queda intacto:** no se edita `llm_providers.py`
  ni `llm_api_service.py` ni `ai_embedding.py` del core; el registro y los
  parches viven en el módulo local `mofgw_ai` (P1, P4-P7). `git diff` del módulo
  local no toca ningún archivo de `enterprise/ai`.
- **I2 — Sin regresión en openai/google:** cada parche solo agrega la rama
  `'mofgw'` y delega al método original para los otros providers (P4-P7).
- **I3 — mofgw se sirve por el path OpenAI-compatible:** `_request_llm` → `/responses`,
  `get_embedding` → `/embeddings`, tool calls formato openai — consistente con
  mofgw (011-001, 011-006). No hay un flujo "native" nuevo para mofgw.
- **I4 — Embeddings consistentes en dimensión:** agentes mofgw producen
  `embedding_model='all-minilm'` (P3) y vectores 384-dim, alineados con la
  columna `vector(384)` de 011-007. Sin esto, el operador `<=>` fallaría por
  dimensión.
- **I5 — `embedding_model` re-declarado sin perder atributos del core:** el
  merge con `selection_add` (D3) conserva `required=True` y los valores
  openai/google del Selection (P10).
- **I6 — Config por instancia, no por compañía:** `ai.mofgw_url`/`ai.mofgw_key`
  son `ir.config_parameter` globales (mismo patrón que `ai.openai_key`).

## Criterios de aceptación

- **C1 (P1, P2):** con `mofgw_ai` instalado, un test de módulo Odoo verifica que
  `PROVIDERS` contiene el Provider `'mofgw'` con los 5 modelos, y que
  `get_provider(env,'deepseek-v4-flash')=='mofgw'`,
  `get_provider_for_embedding_model(env,'all-minilm')=='mofgw'`, y que
  `_get_llm_model_selection()` incluye los 5 modelos mofgw.
- **C2 (P3, I4):** para un agente `ai.agent` con `llm_model='deepseek-v4-flash'`,
  `agent._get_embedding_model() == 'all-minilm'`.
- **C3 (P4-P7):** un test de módulo Odoo verifica, con mock de `_request` /
  env vars, que: `base_url` de mofgw == `ai.mofgw_url` o default D5; `_get_api_
  token` resuelve config→env→UserError; `_request_llm('mofgw')` dispara
  `_request_llm_openai`; `_build_tool_call_response('mofgw')` == formato openai.
  Y que openai/google retornan su comportamiento original (I2).
- **C4 (P8):** `get_embedding` con `provider='mofgw'`, `model='all-minilm'`,
  `dimensions=384` invoca `_request` sobre `<base_url>/embeddings` con el Bearer
  de `ai.mofgw_key` (mock verificando método, endpoint y header).
- **C5 (P9):** `res.config.settings` expone `mofgw_url`/`mofgw_key`; guardar
  setea `ir.config_parameter` `ai.mofgw_url`/`ai.mofgw_key`; la view heredada
  existe (registro `ir.ui.view` con `inherit_id=base.res_config_settings_view_form`).
- **C6 (P10):** `ai.embedding._fields['embedding_model'].get_values(env)`
  contiene `('all-minilm', 'All-minilm')`, conservando `required=True`.
- **C7 (P11, E2E):** en la instancia staging con mofgw real y key configurada,
  una llamada `request_llm('deepseek-v4-flash', ...)` contra
  `LLMApiService(env, provider='mofgw')` retorna respuestas; y `get_embedding(
  model='all-minilm', dimensions=384)` retorna un vector 384-dim.
- **C8 (I1):** `git diff` del módulo local `mofgw_ai` no toca archivos del core
  `enterprise/ai`.

### Mapeo de pruebas de contrato (Etapa 3)

Como es Odoo (Python + monkeypatch + config), los tests de módulo verifican el
contrato observable: estado de `PROVIDERS`, comportamiento de los métodos
parcheados (vía mock de `_request`/env), Selection de `ai.embedding`, y settings.
No dependen de estructura interna del core (solo del comportamiento público).

| Postcondición | Verificación observable                                                                                                              |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| P1, P2        | `PROVIDERS` tiene Provider 'mofgw' (5 llms, embedding 'all-minilm'); `get_provider*` resuelven; `_get_llm_model_selection()` incluye los 5 |
| P3, I4        | `agent._get_embedding_model()=='all-minilm'` para agente mofgw                                                                         |
| P4            | `LLMApiService(env,'mofgw').base_url` == `ai.mofgw_url` o default D5                                                                     |
| P5            | `_get_api_token()`: config → env → UserError; openai/google intactos                                                                   |
| P6            | `_request_llm('mofgw')` delega a `_request_llm_openai` (mock)                                                                            |
| P7            | `_build_tool_call_response('mofgw')` == formato openai                                                                                 |
| P8            | `get_embedding('mofgw')` → `_request` POST `<base>/embeddings` con Bearer mofgw                                                            |
| P9            | settings expone `mofgw_url`/`mofgw_key`; setea config_parameter; view heredada                                                           |
| P10           | `ai.embedding` Selection contiene `('all-minilm','All-minilm')`, `required=True`                                                           |
| P11, C7       | E2E en staging: `request_llm` y `get_embedding(384)` contra mofgw real                                                                   |

## Contexto técnico

**Modelos/entidades tocadas:**
- `enterprise/ai/utils/llm_providers.py`: `Provider` NamedTuple (:7-11),
  `PROVIDERS` (módulo-global `list`, :14-40, compartido por referencia),
  `EMBEDDING_MODELS_SELECTION` (snapshot por comprensión, :43-45),
  `get_provider_for_embedding_model` (:48-52), `get_provider` (:55-58). NO se
  editan; `PROVIDERS.append` se hace desde el módulo local.
- `enterprise/ai/utils/llm_api_service.py`: `LLMApiService.__init__` (:87-98,
  `base_url` por provider), `_get_api_token` (:208-226, `config_key`+`env_var`),
  `_request_llm` (:508-515, despacho), `_build_tool_call_response` (:672-696,
  formato por provider), `get_embedding` (:100-121), `_request_llm_openai`
  (:267, reutilizado), `_to_open_ai_tool_schema` (:652-670). NO se editan; se
  parchean in-place.
- `enterprise/ai/models/ai_embedding.py`: campo `embedding_model` (Selection
  con `EMBEDDING_MODELS_SELECTION`, :30) — se overridea con `selection_add`
  (D3). El override del vector ya lo hace 011-007 (mismo módulo).
- `enterprise/ai/models/ai_agent.py`: `_get_llm_model_selection` (:262-266,
  lee `PROVIDERS`), `_get_provider` (:366-368), `_get_embedding_model`
  (:370-376), `_build_rag_context` (:588-624, usa `get_embedding` +
  `_get_dimensions()`). NO se editan.
- `enterprise/ai/models/res_config_settings.py`: patrón `config_parameter` para
  `ai.openai_key`/`ai.google_key` (:14-32) — espejo para `mofgw_url`/`mofgw_key`.
- `enterprise/ai/views/res_config_settings_views.xml`: view heredada de
  `base.res_config_settings_view_form` con xpath `//block[@name='integration']`
  — patrón a replicar para mofgw.

**Hooks/extensión disponibles:**
- Import del módulo `mofgw_ai/__init__.py` (ya existe; se agrega el
  `PROVIDERS.append` + los parches). Odoo carga el paquete del módulo al
  instalarlo → el monkeypatch corre en install/upgrade.
- `_inherit='ai.embedding'` (ya presente) → override con `selection_add` para
  `embedding_model` (D3).
- `res.config.settings` `_inherit` + view `res_config_settings_view_form` →
  settings del operador (D4).

**Convenciones aplicables:**
- API keys solo en `ir.config_parameter` con fallback a env var (mismo patrón
  `ai.openai_key`/`ODOO_AI_CHATGPT_TOKEN`, `_get_api_token:208-226`).
- Monkeypatch de métodos de clase capturando el original y delegando (evita
  romper openai/google).
- `embedding_model` forzado por provider (mofgw → `'all-minilm'`), consistente
  con la dimensión 384 de 011-007.

**Verificaciones pendientes (deuda documentada, no bloqueante):**
- VERIFICAR: `_to_open_ai_tool_schema` (llm_api_service.py:652) retorna el schema
  **sin transformar** para `provider != 'openai'` (mofgw no expande `required` ni
  convierte `type` a array). mofgw (011-003) acepta el schema como viene; si
  algún upstream exige la forma openai estricta, se documentaría como ajuste
  futuro. No bloqueante: mofgw es el destino y traduce en su capa.
- VERIFICAR: los `display_name` exactos de los 5 modelos (D2 fija los ids; los
  labels del dropdown `llm_model` son decisión de UX de implementación).
- VERIFICAR: el merge de `fields.Selection(selection_add=...)` conserva
  `required=True` y los valores del core — patrón estándar Odoo; se confirma en
  el test C6 (P10).
- VERIFICAR (E2E): la llamada real contra mofgw (`ai.mofgw_url` en staging) —
  P11/C7 se verifica empíricamente en la instancia, no en esta máquina.

## Notas de implementación (orientación, no vinculante)

- En `mofgw_ai/__init__.py`: agregar (tras `from . import models`) el
  `PROVIDERS.append(Provider('mofgw', ...))` y los 4 parches de
  `LLMApiService`, importando `os`, `UserError`, `_` y las referencias al
  original. NO tocar el pre_init_hook existente.
- `models/ai_embedding.py`: agregar `from odoo import fields` y la re-declaración
  `embedding_model = fields.Selection(selection_add=[('all-minilm',
  'All-minilm')])`. NO tocar `embedding_vector`.
- Nuevo `models/res_config_settings.py` (`_inherit='res.config.settings'`, fields
  `mofgw_url`/`mofgw_key` con `config_parameter`) y registrarlo en
  `models/__init__.py`.
- Nueva view XML (p.ej. `views/res_config_settings_views.xml`) heredando
  `base.res_config_settings_view_form` con xpath `//block[@name='integration']`,
  replicando el `setting` de openai/google (res_config_settings_views.xml:9-25).
  Agregar el archivo a `'data': [...]` del `__manifest__.py`.
- Los parches resuelven `ai.mofgw_url`/`ai.mofgw_key` con `sudo()` (mismo patrón
  que el core `_get_api_token`). Default D5 `http://127.0.0.1:3369/v1`.
- El `embedding_model` del Provider mofgw debe ser EXACTAMENTE `'all-minilm'`
  (igual al valor agregado en `selection_add`) para que `get_provider_for_
  embedding_model` y `_get_embedding_model` resuelvan consistente.

## Out of scope

- **`ir_actions_server.AI_PROVIDER` (D6, deuda documentada):** el path de server
  actions (`state='ai'`, `AI_PROVIDER="openai"`/`AI_MODEL="gpt-4.1"`,
  ir_actions_server.py:26-27) queda fuera de scope. El path principal cubierto
  es `ai.agent` (`get_provider` + `LLMApiService(provider=...)`). Hacer que las
  server actions usen mofgw requiere parchear ese atributo de clase; queda como
  deuda del epic.
- **Subclase de `LLMApiService`:** el core instancia la clase por nombre; se usa
  monkeypatch in-place (D1), no subclase.
- **Audio / realtime** (`/v1/audio/*`, `/v1/realtime/*`) — deuda del epic; no se
  tocan (mofgw no los sirve).
- **Cambio de provider a nivel de server actions** (ver D6).
- **Ajuste de `_to_open_ai_tool_schema` para mofgw** — se conserva el
  comportamiento actual (schema sin transformar); solo se documenta.
- **Edición del core `enterprise/ai`** (I1): ningún archivo del core se toca.

## Cambios

- 2026-08-12: draft inicial (architect, Etapa 2). Decisiones D1-D6 resueltas por
  el dueño delegado. Hallazgo verificado: `PROVIDERS` es compartido por
  referencia (get_provider/get_provider_for_embedding_model/_get_llm_model_
  selection lo ven); `EMBEDDING_MODELS_SELECTION` es snapshot separado → override
  `selection_add` obligatorio (D3); `get_embedding` funciona con solo parchear
  `__init__` + `_get_api_token` (base_url + Bearer). ÚLTIMA feature del epic 011.

---

Status: Approved by Ofap (agent-delegated, pedido explícito de Pablo el 2026-08-12 "eres el user dueño hitl, avanza y toma las mejores decisiones con criterio") on 2026-08-12
