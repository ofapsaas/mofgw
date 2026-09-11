# Epic 019 — provider-sync-automation

**Fecha:** 2026-09-11 · **Aprobado por:** Ofap (agent-delegated HITL, pedido explícito de Pablo — goal mode)
**Fuente de análisis:** estudio 2026-09-11 del mecanismo intrínseco de opencode (models.dev + Zen/Go), documentado en esta conversación y resumido acá.

## Problema

mofgw mantiene `config.yaml` (providers[].models, pricing, model_metadata) a mano.
El catálogo upstream (models.dev + listas autorizadas Zen/Go) cambia sin aviso y
el skill `mofgw-provider-sync` documenta un workflow manual que se ejecuta rara vez
y con error humano. opencode resuelve lo mismo con fetch+cache+lock+TTL+snapshot;
queremos replicar ese mecanismo nativamente en mofgw.

## Alcance

- **Dentro:** automatizar provider-sync del lado mofgw (fetch → merge → validate → write → reload), ejecutable manual y por timer.
- **Fuera (explícito):** actualización de clientes (opencode/openclaw/zot). Los clientes ya tienen contrato pull publicado (`GET /v1/client-config`, Epic 016) y skill `mofgw-client-sync`; pueden ser remotos y el binario de mofgw no debe tocarlos. La epic termina cuando `GET /v1/models` refleja paridad con upstream tras reload.

## Fuentes de verdad upstream

| Fuente | Endpoint | Provee |
|---|---|---|
| models.dev | `GET https://models.dev/api.json` | metadata completa (cost, limit, modalities, tool_call, reasoning_options, variants) |
| opencode Zen | `GET https://opencode.ai/zen/v1/models` | IDs autorizados Zen (OpenAI-compatible, sin auth probada pública) |
| opencode Go | `GET https://opencode.ai/zen/go/v1/models` | IDs autorizados Go |
| OpenRouter | `GET https://openrouter.ai/api/v1/models` | ids + context_length + pricing (provider adicional ya en config) |

Regla (skill): el catálogo upstream manda; mofgw es copia verificada, no declaración. Capability no declarada por upstream → omitida.

## Features (orden de ejecución)

| # | Feature | Resumen | Depende de |
|---|---|---|---|
| 019-001 | fetch-modelsdev | Fetch `models.dev/api.json` con cache en disco (TTL), flock cross-process, timeout+retry, User-Agent propio | — |
| 019-002 | fetch-zen-go | Fetch listas autorizadas Zen/Go/OpenRouter (condicional a API keys configuradas) | — |
| 019-003 | merge-provider-catalog | Merge: filtrar models.dev por IDs autorizados por provider + mapeo fijo a providers[].models / pricing / model_metadata | 001, 002 |
| 019-004 | atomic-write-validate | Write atómico de config.yaml (temp+rename, preservando secciones no-tocadas) + validación `config.Parse` pre-commit | 003 |
| 019-005 | reload-signal | Señalizar reload a mofgw (SIGHUP si hot-reload disponible; restart si cambia providers[]) con verificación post-reload (`GET /v1/models`) | 004 |
| 019-006 | systemd-timer | Unidad timer 60 min + OnBootSec + logging estructurado + modo `--once` | 005 |
| 019-007 | build-snapshot | Snapshot embebido del catálogo en build (fallback offline, patrón opencode models-snapshot) | 001 |

## Contratos cross-feature

- 003 consume la salida tipada de 001 (catálogo models.dev parseado) y de 002 (set de IDs por provider). La interfaz se define en el spec de 003 y los specs de 001/002 la anticipan (los tests de RED congelan el contrato).
- 004 es el único componente que escribe `config.yaml`; 003 produce el IR (ConfigIR-like) que 004 serializa. Patrón análogo a clientconfig (Epic 016): IR → renderer.
- 005 solo corre si 004 validó. Falla de validación = abort sin escribir (fail-loud).

## Decisión de arquitectura clave (a validar en discovery de 019-001)

Forma del entregable: **binario/subcomando `mofgw-sync`** (o subcomando de mofgw) separado del servidor — el sync corre desde cron/timer y desde CLI, el servidor mofgw permanece simple. Alternativa (endpoint admin in-server) queda fuera salvo que discovery la justifique.

## Riesgos

- `models.dev/api.json` es multi-MB y cambia de schema: parseo tolerante + digest sha256 para skip de writes byte-idénticos (lección del PR #44282 de opencode).
- Zen/Go `/models` lista IDs "de acceso" pero NO pricing — pricing SIEMPRE viene de models.dev; si un ID no está en models.dev, se omite con warning (fail-soft) y se registra en el log del sync.
- Reload: Epic 017 (hot-reload) está pausada → 019-005 default = restart del servicio systemd; se re-evalúa cuando 017 retome.
- Escritura de config.yaml debe preservar comentarios si el archivo los tiene (yaml.v3 no los preserva en round-trip) → evaluar: emitir config regenerada en secciones acotadas vs. edición estructural. Decisión en spec de 019-004.

## Criterio de cierre de la epic

- `mofgw-sync --once` ejecuta el ciclo completo contra upstreams reales y `GET /v1/models` local refleja paridad de IDs con las fuentes autorizadas.
- Timer systemd activo con logs del sync.
- Suite Go completa verde (-race), sin regresiones (baseline actual: 733 tests / 29 pkgs).
