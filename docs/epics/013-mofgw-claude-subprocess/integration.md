# Integración — EPIC-013-mofgw-cli-subprocess

**Etapa:** E3 — Integración cross-feature.
**Fecha:** 2026-08-12
**Estado:** 4/4 features done. Suite **581 tests `-race` en 22 paquetes** verde, `go vet` limpio.

## Features del epic

| Feature | Commit(s) clave | Qué entregó |
|---------|-----------------|-------------|
| 013-001-subprocess-core | 7d6737d, d07de8c, b9cbd33 | Motor `subprocess` genérico + interfaz `Backend` (session por cliente, serialización, exec, usage, errores ErrUpstream) |
| 013-002-adapter-claude | d20c293, b8a211a, e5ec87d | Adapter `claude.Backend` (argv, traducción stream-json, text-only, refusal) |
| 013-003-config-wiring | 5e14760, fe9dfe5, e21fa77 | Config `type: subprocess`, factory `internal/providerfactory`, catálogo no-tools, restricción router |
| 013-004-resilience-ops | 7ce359f, c2c3fd8 | TTL sesiones, usage preservado, sc.Err, env allowlist, ctx-guard stream, IsRefusal |

## Flujo cross-feature (E2E)

```
cliente OpenAI chat-completions
  → auth.Wrap (clientID)
  → proxy handleChat (body crudo transparente)
  → router.resolveReady(model, clientID)  [013-003: filtra AllowedClients + cooldown]
  → candidate subprocess.Provider [013-001: motor]
  → Complete/Stream [013-001]
  → claude.Backend [013-002: argv + traducción]
  → spawn CLI (stub en tests / claude real en smoke manual) [013-001 childEnv allowlist, 013-004]
  → respuesta → chat-completions [013-002 TranslateOut/TranslateStreamOut]
  → catálogo /v1/models lista el modelo no-tools [013-003, via Models()]
```

## Verificación E2E cross-feature

- **E2E principal (stub CLI):** `TestT_RequestToolsNotReachCli` (proxy/e2e_007002-style) ejercita el path completo proxy→router→subprocess.Provider→claude.Backend→stub CLI: un request con `tools` a un modelo subprocess completa 200 y el CLI **no** recibe tools (D4/OMIT). Verde.
- **E2E catálogo:** `TestT_CatalogListsSubprocessModels`/`_NoTools`/`_NoMetadata` — /v1/models lista modelos subprocess, no-tools (declarativo), mínimo sin metadata. Verde.
- **E2E restricción:** `TestT_RestrictionAllowedClient`/`_DeniedClientFallback`/`_DeniedClient404` + `_DeniedCooldownGraceNoWait` — cliente autorizado rutea, denegado → fallback/404 (nunca 403), sin cooldown espurio. Verde.
- **E2E sesión/serialización:** 013-001 tests (mismo clientID misma sesión, dos clientes aislados, serialización por sesión). Verde.
- **E2E resiliencia:** 013-004 tests (usage preservado, ctx-guard libera lock, TTL limpia sesiones viejas, env allowlist). Verde.

## Smoke test real (manual, fuera de la suite)

El CLI `claude` autenticado y la suscripción no están disponibles en el entorno de test (hermeticidad). La verificación con el CLI real es manual (D6), y cubre: `--session-id` sha256 hex aceptado, prompt vía stdin, `--output-format stream-json`, y los markers reales de refusal (B-Q7). Pendiente para el operador.

## Gate E3 → E4

- [x] Suite completa verde con `-race` (581/22) y `go vet` limpio.
- [x] Tests E2E cross-feature presentes y verdes (flujo completo vía stub CLI).
- [x] 4/4 features del epic done, cada una con Memory Bank/activeContext.

Status: E3 verificado — 0 bloqueantes. Aprobado por Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12.
