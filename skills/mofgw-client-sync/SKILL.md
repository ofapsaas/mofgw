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
| `opencode` | `~/.config/opencode/opencode.json` o `./opencode.json` | `provider.mofgw` (o `models.providers.mofgw`) + `models[]` de ese provider | resto del json del usuario (otros providers, `agents.defaults`, etc.) |
| `openclaw` | `~/.openclaw/openclaw.json` | bloque `models.providers.mofgw` | resto |
| `zot` | según `zot.conf` | bloque del provider mofgw | resto |

> Cliente no listado → 404 con lista de soportados (comportamiento del endpoint). No improvisar formato — pedir que se agregue renderer en mofgw.

## Precondiciones (verificar antes de tocar disco)

```bash
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
  "http://127.0.0.1:3369/v1/client-config?client=opencode" -o /tmp/mofgw-opencode-fragment.json
jq . /tmp/mofgw-opencode-fragment.json
# Verificar: base_url correcto, key es env-ref (ej. "{env:MOFGW_KEY}" o "$MOFGW_KEY"), nunca literal
```

### 2. Backup
```bash
cp ~/.config/opencode/opencode.json ~/.config/opencode/opencode.json.bak.$(date +%Y%m%d%H%M%S)
```

### 3. Merge quirúrgico

Reemplazar **solo** el provider mofgw. No reordenar ni borrar otros providers.

Pseudo-merge para `opencode.json` (ajustar key según shape real del renderer):
```bash
# El fragmento ya viene con la forma exacta del cliente — insertar bajo la key que use el renderer
# Ejemplo opencode: el fragmento es el objeto del provider mofgw listo para providers.mofgw
python3 -c "
import json, os
with open('/tmp/mofgw-opencode-fragment.json') as f: frag=json.load(f)
with open(os.path.expanduser('~/.config/opencode/opencode.json')) as f: cfg=json.load(f)
# El renderer de opencode emite el provider completo — reemplazar solo esa key:
cfg['providers'] = cfg.get('providers', {})
cfg['providers']['mofgw'] = frag  # si el fragmento es el provider; si es {providers:{mofgw:{...}}} adaptar
# Alternativa si el fragmento ya es {\"mofgw\": {...}}: cfg['providers'].update(frag)
with open(os.path.expanduser('~/.config/opencode/opencode.json'),'w') as f: json.dump(cfg,f,indent=2)
print('merged')
"
```

> Si el shape no calza, inspeccionar el fragmento con `jq 'keys'` — el renderer es la verdad, no este ejemplo.

### 4. Validar
```bash
python3 -m json.tool ~/.config/opencode/opencode.json > /dev/null && echo "JSON ok"
# Verificar que la key sigue siendo env-ref, no literal:
grep -q "MOFGW_KEY" ~/.config/opencode/opencode.json && echo "env-ref ok" || echo "FAIL: key literal?"
```

### 5. Aplicar
```bash
# opencode: reiniciar si es necesario (algunos setups leen en arranque)
# openclaw: systemctl --user restart openclaw-gateway
```

## Invariantes (no negociables)

- Key **nunca** literal en disco — siempre env-ref (`MOFGW_KEY` default de `client_config.key_env`).
- No agregar modelo que no esté en el fragmento (que a su vez es `/v1/models`).
- No tocar `agents.defaults.model` / routing sin aprobación explícita (ver skill `opencode-providers` para convención `acct1-go`/`acct2-go`).
- Idempotente: correr dos veces sin cambio upstream → diff vacío.

## Fallos esperados

| Síntoma | Causa | Acción |
|---------|-------|--------|
| `503 client_config.base_url not set` | knob vacío en mofgw | setear `client_config.base_url` y reload mofgw |
| `404 unsupported client` | renderer no existe | usar solo `opencode`/`openclaw`/`zot` o agregar renderer en mofgw |
| `401` | `Authorization` faltante/inválida | usar key de un `clients[].id` válido de mofgw |

## Output esperado

Diff del archivo del cliente (solo provider mofgw) + `jq` del fragmento que muestra paridad con `/v1/models`.
