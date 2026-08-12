# Closure — EPIC-013-mofgw-cli-subprocess

**Fecha:** 2026-08-12
**Estado:** ✅ CERRADO — 4/4 features done, integración E3 verificada, ADR-010.

## Resumen

Epic 013 agregó a mofgw un provider tipo `subprocess` que ejecuta el CLI de un backend de IA (claude) como subproceso, permitiendo usar la suscripción Claude Pro como provider de fallback sin reverse-engineering de OAuth ni bridges HTTP. Arquitectura: motor genérico (`internal/subprocess`) + interfaz `Backend` + adapter `claude` + wiring de config/factory/catálogo/restricción + hardening de resiliencia.

## Features entregadas (4/4)

| # | Feature | Estado |
|---|---------|--------|
| 013-001 | Motor subprocess genérico + interfaz Backend (sesión por cliente, serialización, exec, usage, errores) | ✅ DONE |
| 013-002 | Adapter claude.Backend (argv, traducción stream-json, text-only, refusal) | ✅ DONE |
| 013-003 | Wiring: config `type: subprocess`, factory `internal/providerfactory`, catálogo no-tools, restricción router | ✅ DONE |
| 013-004 | Resiliencia: TTL sesiones, usage preservado, sc.Err, env allowlist, ctx-guard stream, IsRefusal | ✅ DONE |

## Decisiones arquitectónicas

- **ADR-010 — Provider subprocess con frontera Backend:** motor genérico + interfaz `Backend` (argv/traducción/parseo/refusal en el adapter; el motor no conoce strings backend-específicos). `docs/adr/ADR-010-provider-subprocess-backend.md`.

## Criterios de aceptación verificados

- 4/4 features done individualmente (cada una con spec→RED→GREEN→review→merge).
- E2E principal (stub CLI): request a modelo subprocess completa; tools OMIT; catálogo no-tools; restricción por cliente con fallback; serialización por sesión; resiliencia (TTL, ctx-guard). **Suite 581 tests `-race` en 22 paquetes** verde, `go vet` limpio.
- Smoke test real con el CLI `claude` autenticado: **pendiente manual** (hermeticidad; fuera de suite).

## Retrospectiva breve

- El patrón de sesión (por cliente, prompt único, CLI sostiene historial) se consolidó tras iterar sobre la alternativa "prompt-cache full-context" — la sesión-sostiene-historial es más simple y robusta para el caso proxy.
- El cambio de interfaz `TranslateStreamOut(ctx,...)` (013-004) fue necesario para el guard de ctx — se decidió por interfaz (context-first en Go) sobre un wrapper del engine (que traslada el leak).
- 4 features entregadas en una sesión con ciclo CDAD completo por feature, delegación a sub-agentes aislados, y 0 bloqueantes de review en las 4.

## Deuda técnica que se llevó

- **Smoke test real con el CLI claude** (formato --session-id sha256, prompt vía stdin, markers reales de refusal B-Q7) — manual, pendiente operador.
- Health check proactivo subprocess (diferido con rationale: señales solo en request real que quema cuota; el router absorbe fallos vía P10+fallback).
- Mapeo específico rate-limit→429 / auth→401/403 (necesita datos del CLI real).
- Allowlist de env configurable (hoy fija PATH+HOME+STUB_*; mitiga sombreado de ANTHROPIC_API_KEY).
- Passthrough de tool_calls OpenAI↔CLI y structured output (deuda del epic, CLI usa sus propias tools).
- 10 nits/FYI de la review de 013-004 (polish: logging de dirMtime, warning usage negativo, doc lastSweep).
- Adapters gemini/codex futuros (interfaz Backend lista, sin tocar el motor).
- Knob `session_key: client+model` (requiere tocar el motor; default client solo).

## Referencias

- Plan: `docs/epics/013-mofgw-claude-subprocess/plan.md`
- Integración: `docs/epics/013-mofgw-claude-subprocess/integration.md`
- ADR: `docs/adr/ADR-010-provider-subprocess-backend.md`
- Specs: `docs/specs/013-001-subprocess-core/`, `013-002-adapter-claude/`, `013-003-config-wiring/`, `013-004-resilience-ops/`

Status: Epic 013 CERRADO por Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12.
