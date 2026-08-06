# Handoff — Reviewer 003-001-provider-cache (Etapa 4)

> Generado: 2026-08-06. Estado: **BLOQUEADO por runtime de delegación caído**
> (10+ fallos "Failed to create delegation session" en delegate Y task,
> 13:30-14:00). El reviewer NO se ejecutó inline a propósito: es el rol con
> invariante anti-bias no negociable (familia de modelo distinta al
> implementer). Cuando el runtime se recupere → delegar este packet a
> `cdad-reviewer`. Alternativa: chat nuevo con el mismo contenido.

## Cómo retomar

1. Verificar `go test ./... -race` verde (debería estarlo: 13 paquetes, 0 FAIL).
2. Delegar este packet a `cdad-reviewer` (o abrir chat nuevo y pegarlo).
3. Materializar `docs/specs/003-001-provider-cache/review.md` desde el output.
4. Validar Gate 4→5 (bloqueantes resueltos, suite verde) → Etapa 5 (merge + memory bank).

## Packet del reviewer

---
You are the CDAD reviewer (Etapa 4, two-layer review) for feature 003-001-provider-cache in the mofgw project (Go OpenAI-compatible gateway at /home/ofap/clawd/projects/mofgw). You are READ-ONLY. Your job: find findings against the spec, severity-classified.

## Context
Feature: instrument provider-side cache hit/miss (parse cached_tokens/reasoning_tokens, inject include_usage in streaming, counters in /metrics, log fields in request_end, robustness when usage absent).

## What to review
1. The spec: /home/ofap/clawd/projects/mofgw/docs/specs/003-001-provider-cache/spec.md (postconditions P1-P6, invariants I1-I4, criteria C1-C6).
2. The implementation (commits 4bc9e29 + 52da75b):
   - internal/provider/provider.go (Usage struct + UnmarshalJSON + usageInt)
   - internal/metrics/metrics.go (IncCacheTokens + Render tokens section)
   - internal/router/router.go (ensureIncludeUsage + Stream wiring)
   - internal/proxy/proxy.go (recordCacheTokens, emitRequestEnd, handleStream, CaptureUsage wiring)
   - internal/stream/stream.go (CaptureUsage/CapturedStream)
3. The tests: internal/provider/provider_test.go (TestCompleteParseCacheFields*), internal/proxy/e2e_003001_test.go (all TestRED_* + TestE2E_StreamUsageCapturadoEnMetrics), internal/metrics/metrics_test.go (TestNamesSorted update).
4. Run `go test ./... -race` and `go vet ./...` to verify (report results).

## Two-layer review
Layer 1 — Contract correctness: do the postconditions hold? Is each C1-C6 satisfied? Any behavioral gap (e.g. stream logs, privacy, transparency)?
Layer 2 — Code quality: concurrency (race detector already run — but check the CapturedStream usage-after-drain synchronization semantics), error handling (usageInt, ensureIncludeUsage error paths), metrics cardinality, consistency with existing patterns (clamp, rejected map, Copy).

## Severity matrix
- Blocker: violates spec postcondition/invariant; security/privacy leak; data corruption; suite red.
- Major: violates intent; edge case unhandled that will bite in production; API misuse risk.
- Minor: style, naming, docs, test clarity.
- Nit: trivial.

## Output format (final message, markdown)
# Review — 003-001-provider-cache
## Verdict: APPROVE / REQUEST CHANGES (blocker/major count)
### Findings
| # | Severity | Postcondition/Section | Finding | Evidence (file:line) | Suggested fix |
### Positives
### Test suite verification

Be precise with file:line evidence. Do NOT modify any files.
---
