# Test Audit — 013-003-config-wiring

**Feature:** wiring del provider subprocess (config type:subprocess, factory, catálogo no-tools, tools OMIT, restricción router). Epic 013-mofgw-cli-subprocess.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec:** `docs/specs/013-003-config-wiring/spec.md` (aprobado por Ofap, P1-P10 + C1-C10 + tabla §4.2).
**Nuevo:** 16 tests (5 config / 3 factory / 3 catalog e2e / 1 request-path e2e / 4 router restriction).

## A) Baseline — se usan, NO se re-testean

Config load/validate HTTP, router candidate/fallback/cooldown, auth.ClientIDFrom, handleModels/modelCatalogEntry (HTTP), motor subprocess (013-001), adapter claude (013-002) → todos con cobertura existente. **Net-new:** ProviderConfig.Type + campos subprocess, buildProvider/buildBackend, ProviderSpec.AllowedClients + filtro, handleModels→interface{Models()}, (*subprocess.Provider).Models() accessor.

## B) Plan de tests nuevos — 16 (tabla §4.2)

Confirmados: necesarios, mapean a postcondición/criterio, verifican comportamiento observable. Los 2 flags resueltos por el owner:

1. **T_config_unknown_backend_error**: validación de backend en tiempo de carga (config.validate rechaza `backend != "claude"`), alineado con C4/P3. buildBackend queda defensivo.
2. **T_build_* (factory)**: buildProvider/buildBackend viven en **`internal/providerfactory`** (importable). main.go lo llama como adapter fino.

## C) Harness

- Stub CLI (POSIX script, stdin→STUB_STDIN_FILE) + adapter claude real → request path e2e (tools no llegan).
- Config fixture YAML con type:subprocess (sin key).
- Router restriction: clientCtx vía auth.Wrap + ProviderSpec.AllowedClients directo; fallback denegado assert callCount()==0.
- Proxy catalog e2e: helper buildWithSubprocess (providers = HTTP ∪ subprocess).

## Gate — Test Audit

- [x] Spec aprobado (P1-P10, C1-C10, tabla §4.2).
- [x] Net-new identificado (config/factory/catalog/restriction).
- [x] Contratos compartidos rely.
- [x] 0 tests a modificar; suite existente untouched.
- [x] 16 tests nuevos mapeados, de comportamiento observable.
- [x] Sin mocks sobre plumbing; sin duplicación.
- [x] Harness hermético (stub CLI + command inyectable).

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
