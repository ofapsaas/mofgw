---
name: mofgw-provider-sync
description: |
  Workflow determinístico para actualizar la config de mofgw (providers/pricing/model_metadata) desde los endpoints reales de los proveedores conocidos (opencode Zen/Go, OpenRouter). Usar cuando: cambian modelos/precios/capacidades upstream, hay que agregar/quitar un provider, o el catálogo /v1/models está stale. Evita investigación abierta — el skill fija fuentes, mapeos y validación.
---

# mofgw-provider-sync

Sincroniza `~/.config/mofgw/config.yaml` (o `/etc/mofgw/config.yaml`) desde la **fuente de verdad upstream**. No inventa capacidades.

## Principio

> El catálogo upstream manda. `pricing` y `model_metadata` en mofgw son copia verificada, no declaración.

Regla vigente (2026-08-30): capability **declarada por el provider → se confía y se incluye** tal cual. No se verifica contra docs externas ni se omite por falta de verificación. Solo se omite si el provider no la declara.

## Proveedores conocidos

| ID en mofgw | Base URL | Endpoint catálogo | Auth |
|-------------|----------|-------------------|------|
| `opencode-zen` | `https://opencode.ai/zen/v1` | `GET /models` | `Authorization: Bearer $MOFGW_PROVIDER_*_KEY` si requiere, o sin auth si es público |
| `opencode-go` | `https://opencode.ai/zen/go/v1` | `GET /models` | idem |
| `openrouter` | `https://openrouter.ai/api/v1` | `GET /models` | `Authorization: Bearer $OPENROUTER_API_KEY` |

> Si el proveedor no está en esta tabla, no es "conocido" — requiere research previo y actualizar este skill, no improvisar.

## Workflow (ejecutar en orden, sin saltos)

### 1. Snapshot actual
```bash
cat ~/.config/mofgw/config.yaml  # o /etc/mofgw/config.yaml
curl -s http://127.0.0.1:3369/v1/models | jq '.data[] | {id, context_length, max_completion_tokens}'
```

### 2. Fetch upstream (fuente de verdad)
```bash
# Zen / Go — verificar modelos reales (no asumir lista del skill opencode-providers)
curl -s https://opencode.ai/zen/v1/models | jq '.data[].id' | sort
curl -s https://opencode.ai/zen/go/v1/models | jq '.data[].id' | sort
# OpenRouter — si aplica
curl -s https://openrouter.ai/api/v1/models -H "Authorization: Bearer $OPENROUTER_API_KEY" | jq '.data[] | {id, context_length, pricing}'
```

### 3. Diff y mapeo

Comparar upstream vs `config.yaml:providers[].models` y `model_metadata`/`pricing`.

Mapeos fijos (no reinterpretar):
| Upstream | → mofgw |
|----------|---------|
| `context_length` / `context_window` | `model_metadata.<id>.context_window` |
| `max_output` / `top_provider.max_completion_tokens` | `model_metadata.<id>.max_output` |
| `pricing.prompt` (por 1M) | `pricing.<id>.input_usd_per_m` |
| `pricing.completion` | `pricing.<id>.output_usd_per_m` |
| `pricing.cached` / `input_cache_read` | `pricing.<id>.cache_hit_usd_per_m` |
| `supported_parameters: ["tools","reasoning"]` | tal cual lo declara upstream |
| `modality` (`text->text`, `text+image->text`) | tal cual lo declara upstream |

Pricing/capability no declarada por el provider → se omite (ausente). Precio sin entrada → costo 0.

### 4. Editar config.yaml

Solo tocar:
- `providers[].models` (orden = orden de fallback)
- `pricing.<model>`
- `model_metadata.<model>` (incluye `thinking`/`thinking_default` prescriptivo solo si viene de `research-token-efficiency.md` §4 o ADR-003)

No tocar: `server`, `fallback`, `clients`, `client_config`, `registry`, `embeddings`, `web_search`, `context`.

### 5. Validar (obligatorio, bloqueante)
```bash
mofgw -check-config -config ~/.config/mofgw/config.yaml  # o binario equivalente / mofgw validate
# si falla → no seguir, corregir
curl -s http://127.0.0.1:3369/v1/models | jq length  # tras reload/restart
```

Si el loader reclama `base_url/api_key_env` para un provider `type: subprocess`, el binario es viejo — recompilar (ver `config.example.yaml:77`).

### 6. Reload

- Si Epic 017 hot-reload está deployado y el cambio es solo `model_metadata`/`pricing` → `systemctl --user reload mofgw` o SIGHUP (según implementado).
- Si cambia `providers[]` → `systemctl --user restart mofgw` (cambio de cadena).
- Verificar: `curl -s http://127.0.0.1:3369/v1/models | jq` refleja el nuevo catálogo; `journalctl --user -u mofgw -n 50 --no-pager`.

## Anti-patrones

- No usar `web_search` genérico para precios/capacidades — usar solo los 3 endpoints de arriba.
- No agregar modelo que no aparece en `GET /models` del proveedor.
- No hardcodear `base_url` del cliente acá (eso es `mofgw-client-sync` / `client_config.base_url`).
- No emitir keys literales — siempre `api_key_env`.

## Output esperado

Diff de `config.yaml` + `curl /v1/models` post-reload que muestra paridad con upstream. Si hay discrepancia, documentarla en `docs/research-token-efficiency.md`.
