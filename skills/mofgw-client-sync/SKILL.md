---
name: mofgw-client-sync
description: |
  Workflow determinístico para actualizar la config de clientes conocidos (opencode principalmente) desde mofgw vía GET /v1/client-config. Usar cuando: el catálogo /v1/models cambió, hay que sincronizar opencode.json/openclaw.json/zot con mofgw, o un agente reporta modelos stale. El cliente nunca inventa modelos — consume el fragmento renderizado por mofgw.
---

# mofgw-client-sync

Sincroniza el **cliente** desde la fuente de verdad **mofgw** (endpoint `/v1/client-config`). Inverso de `mofgw-provider-sync`.

## Principio

> mofgw es la verdad del catálogo. El cliente hace merge quirúrgico del fragmento — no reconstruye nada.

- Fuente: `GET /v1/client-config?client=<id>` (renderers de Epic 016: `opencode`/`openclaw`/`zot`).
- El IR (`BaseURL`, `KeyEnvRef`, `Models[]` con metadata completa) ya viene resuelto por mofgw. El skill solo lo inserta.

## Clientes conocidos

| `client` param | Archivo destino | Qué se reemplaza | Qué se preserva |
|----------------|-----------------|------------------|-----------------|
| `opencode` | `~/.config/opencode/opencode.json` **o `opencode.jsonc`** (verificar cuál existe) | `provider.mofgw` (singular) + `models` de ese provider | resto del json del usuario (otros providers, `agents.defaults`, etc.) |
| `openclaw` | `~/.openclaw/openclaw.json` | bloque `models.providers.mofgw` | resto |
| `zot` | según `zot.conf` | bloque del provider mofgw | resto |

> Cliente no listado → 404 con lista de soportados (comportamiento del endpoint). No improvisar formato — pedir que se agregue renderer en mofgw.

> **JSONC:** si el destino es `.jsonc`, verificar si tiene comentarios (`/* */` o `//`). Si parsea como JSON puro, seguir normal. Si tiene comentarios, usar parser JSONC-aware o merge por regex acotado que **preserve los comentarios**; la validación con `python3 -m json.tool` solo aplica al JSON puro.

## Precondiciones (verificar antes de tocar disco)

```bash
# 0. AUTENTICACIÓN DEL CLIENTE (decisión, no opcional):
#    ¿Cómo se autentica el cliente HOY? Inspeccionar (sin leer valores):
#    - Keyring de opencode: ~/.local/share/opencode/auth.json → ¿existe entrada
#      del provider "mofgw"? (leer solo `type` y longitud de la key, NUNCA el valor)
#    - Env: ¿MOFGW_KEY está seteada en el entorno donde corre el cliente?
#      (pgrep + /proc/<pid>/environ, o printenv en la sesión del usuario)
#    REGLA DE DECISIÓN:
#    - Keyring presente → OMITIR options.apiKey del merge (no pisar el keyring).
#    - Sin keyring pero MOFGW_KEY definida → mantener el env-ref del fragmento.
#    - Ni keyring ni env → PARAR y preguntar al usuario (no inventar auth).
#    Para el fetch del fragmento sirve cualquier key de clients[] de mofgw
#    (la auth es por cliente, el fragmento es el mismo para todos).

# 1. mofgw vivo y con base_url configurado (si no → 503 esperado)
curl -s http://127.0.0.1:3369/v1/models | jq '.data[].id' | head
curl -s -H "Authorization: Bearer $MOFGW_KEY" "http://127.0.0.1:3369/v1/client-config?client=opencode" | jq .

# 2. Si 503 "client_config.base_url not set" → setear en config.yaml:
# client_config:
#   base_url: "http://127.0.0.1:3369/v1"  # o https://mofgw.example.com/v1
#   key_env: "MOFGW_KEY"
# y reload mofgw antes de seguir.

# 3. Paridad: modelos del fragmento == modelos de /v1/models
diff <(curl -s http://127.0.0.1:3369/v1/models | jq -r '.data[].id' | sort) \
     <(curl -s -H "Authorization: Bearer $MOFGW_KEY" "http://127.0.0.1:3369/v1/client-config?client=opencode" | jq -r '.models[].id' | sort)
```

## Workflow

### 1. Fetch fragmento
```bash
curl -s -H "Authorization: Bearer $MOFGW_KEY" \
  "http://127.0.0.1:3369/v1/client-config?client=opencode" -o ~/tmp/mofgw-opencode-fragment.json
jq . ~/tmp/mofgw-opencode-fragment.json
# Verificar: base_url correcto, key es env-ref (ej. "{env:MOFGW_KEY}"), nunca literal.
# APLICAR LA DECISIÓN DE AUTH (precondición 0): si el cliente usa keyring,
# omitir options.apiKey del merge — el keyring manda.
```

### 2. Backup
```bash
cp ~/.config/opencode/opencode.jsonc ~/.config/opencode/opencode.jsonc.bak.$(date +%Y%m%d%H%M%S)
```

### 3. Merge quirúrgico

Reemplazar **solo** el provider mofgw. No reordenar ni borrar otros providers.

**Shape real del fragmento (renderer opencode, Epic 016):**
```json
{"mofgw": {"npm": "@ai-sdk/openai-compatible", "name": "mofgw",
           "options": {"baseURL": "...", "apiKey": "{env:MOFGW_KEY}"},
           "models": {"<id>": {"limit": {}, "cost": {}, "reasoning": true,
                               "tool_call": true, "variants": {"low": {}},
                               "x-thinking": {"levels": [], "default": ""}}}}}
```
La key de destino en opencode es **`provider` (singular)**: el objeto interno
`{"mofgw": {...}}` del fragmento se inserta tal cual dentro de `provider`.

```bash
python3 << 'PYEOF'
import json, os
with open(os.path.expanduser('~/tmp/mofgw-opencode-fragment.json')) as f: frag = json.load(f)
path = os.path.expanduser('~/.config/opencode/opencode.jsonc')
with open(path) as f: cfg = json.load(f)
# provider singular; auth por keyring → omitir apiKey (ver precondición 0)
frag['mofgw']['options'].pop('apiKey', None)  # SOLO si keyring decidido en precondición 0
cfg.setdefault('provider', {}).update(frag)
with open(path, 'w') as f: json.dump(cfg, f, indent=2)
print('merged')
PYEOF
```

> ⚠️ **Pérdida de customizaciones:** antes de reemplazar, hacer diff de los
> modelos existentes vs el fragmento. Si algún modelo tiene `options`/
> `variants`/`reasoningEffort` customizadas que el fragmento no trae, **avisar
> al usuario qué se va a perder y confirmar**. El catálogo manda, pero la
> pérdida debe ser explícita, nunca silenciosa.

> Si el shape no calza, inspeccionar el fragmento con `jq 'keys'` — el renderer es la verdad, no este ejemplo.

### 4. Validar
```bash
python3 -m json.tool ~/.config/opencode/opencode.json > /dev/null && echo "JSON ok"
# (solo si el archivo es .json puro; para .jsonc con comentarios, usar el parser JSONC-aware del merge)
# Verificar que NO haya key literal en disco (env-ref o keyring son las dos formas válidas):
grep -q '"apiKey": "sk-' ~/.config/opencode/opencode.json && echo "FAIL: key literal" || echo "ok: sin key literal"
```

### 5. Aplicar
```bash
# opencode: lee config al arranque — validar con `opencode models <provider> --verbose`
# openclaw: systemctl --user restart openclaw-gateway
```

> **Cascada de config de opencode:** opencode deep-mergea config global +
> config de proyecto + `OPENCODE_CONFIG_CONTENT` + plugins. El merge del
> archivo global puede no reflejar la config efectiva — verificar siempre con
> `opencode models <provider> --verbose` (muestra el catálogo resuelto).

## Invariantes (no negociables)

- Key **nunca literal en disco**. Dos formas válidas de cumplirla: env-ref en config (`{env:MOFGW_KEY}`) **o** keyring del cliente (`auth.json`). La tercera — literal en disco — es la única prohibida. (Nota: `openclaw.json` hoy viola esto con key literal — deuda aparte, migrar a env-ref/keyring cuando se haga su sync.)
- No agregar modelo que no esté en el fragmento (que a su vez es `/v1/models`).
- No pisar el keyring del cliente con env-refs si el keyring ya resuelve la auth.
- No tocar `agents.defaults.model` / routing sin aprobación explícita (ver skill `opencode-providers` para convención `acct1-go`/`acct2-go`).
- Idempotente: correr dos veces sin cambio upstream → diff vacío.
- Customizaciones manuales del usuario en el bloque reemplazado: pérdida explícita con confirmación, nunca silenciosa.

## Fallos esperados

| Síntoma | Causa | Acción |
|---------|-------|--------|
| `503 client_config.base_url not set` | knob vacío en mofgw | setear `client_config.base_url` y reload mofgw |
| `404 unsupported client` | renderer no existe | usar solo `opencode`/`openclaw`/`zot` o agregar renderer en mofgw |
| `401` | `Authorization` faltante/inválida | cualquier key de `clients[]` de mofgw sirve (auth por cliente); o keyring del cliente si aplica |
| `{env:MOFGW_KEY}` no resuelve en runtime | env no definida donde corre el cliente | keyring (auth.json) o exportar `MOFGW_KEY`; nunca hardcodear la key |

## Output esperado

Diff del archivo del cliente (solo provider mofgw) + `opencode models <provider> --verbose` mostrando cost/variants/attachment resueltos + paridad con `/v1/models`.
