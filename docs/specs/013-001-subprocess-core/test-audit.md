# Test Audit — 013-001-subprocess-core

**Feature:** motor subprocess genérico (implementa `provider.Provider` spawnando un CLI de backend; interfaz nueva `Backend`). Epic 013-mofgw-cli-subprocess.
**Sub-fase:** Etapa 3.0 — AUDIT.
**Spec:** `docs/specs/013-001-subprocess-core/spec.md` (aprobado por Ofap, P1-P13 + C1-C14 + tabla §4.2 de 19 tests).
**Paquete nuevo:** `internal/subprocess/` — 0 archivos hoy; todo el comportamiento del motor es **net-new**.

## A) Baseline de contratos compartidos — se usan, NO se re-testean

El motor consume contratos existentes que ya tienen cobertura. Los tests nuevos los usan como dependencias, no los re-verifican:

| Contrato | Cobertura existente | Decisión |
|----------|--------------------|----------|
| `provider.Provider` (interfaz) | provider_test.go + router fakeProvider | Rely (compilación + tests del motor) |
| `NewErrUpstream`/`sanitize` | TestSanitizeRedactsURLsAndKeys | Rely (P12 verifica efecto en Message, no re-testa sanitize) |
| `ErrUpstream` + `IsNetwork` | provider_test.go | Rely (motor produce el tipo correcto por fallo, P10) |
| `Usage` | TestCompleteParseCacheFields* | Rely (P6/P7 verifican no-degeneración del Usage fabricado) |
| `auth.ClientIDFrom(ctx)` | auth_test.go | Rely (tests inyectan ctx vía auth.Wrap; P1/P13) |
| `router.classify(err)` | router_test.go (fail400/500/429...) | Rely (motor produce ErrUpstream cuyos campos classify lee) |
| `stream.CaptureUsage`/`Copy` | stream_test.go + e2e | Rely (P7 produce SSE compatible) |

**Nota:** `internal/subprocess/` no tiene suite previa → 0 tests a modificar; paquetes compartidos intactos (motor consumidor aditivo).

## B) Plan de tests nuevos — 19 (mapeo P1-P13, tabla §4.2)

Confirmados test a test: necesarios, mapean a la postcondición/criterio correctos, verifican **comportamiento observable** vía stub CLI + test-double Backend. Ninguno usa mocks sobre plumbing ni duplica tests de tipos compartidos. Los 2 puntos que podrían tentar a mockear plumbing son legítimos: `T_model_passed_per_request` (Args(model) es parte de la interfaz Backend — dato de entrada, no orden de llamadas) y `T_serialize_same_session` (el stub CLI externo cuenta spawns, como prescribe C9).

| Test | P/C | Verifica |
|------|-----|----------|
| T_session_same_client | P1/C2 | mismo clientID → mismo Session.ID |
| T_session_diff_client | P1/C2 | clientIDs distintos → IDs distintos |
| T_session_key_client_model | P2/C3 | override client+model: mismo par⇒mismo ID; difiere del default |
| T_multi_model_serves | P3/C4 | Serves true para todos los modelos listados |
| T_multi_model_same_session | P3/C4 | mismo cliente, modelos distintos → misma sesión (default) |
| T_model_passed_per_request | P3/C4 | modelo llega a Backend.Args (--model) |
| T_single_last_user_prompt | P4/C5 | stdin contiene solo el último user |
| T_prompt_clean_no_tools | P5/C6 | stdin sin tools/tool_calls/internals |
| T_usage_fabricated_complete | P6/C7 | Usage no-degenerado tras Complete |
| T_usage_fabricated_stream | P7/C8 | chunk final con usage extraíble |
| T_serialize_same_session | P8/C9 | máx 1 proceso CLI en vuelo por sesión |
| T_wait_cancellable | P9/C10 | cancelar espera del lock → ErrUpstream; lock intacto |
| T_err_spawn_network | P10/C11 | spawn fallido → Type:network |
| T_err_nonezero_exit | P10/C11 | exit≠0 → ErrUpstream saneado |
| T_err_timeout_retryable | P10/C11 | timeout → Type:timeout (retryable) |
| T_never_leak_ctx_err | P10/C11 | ctx.Err → ErrUpstream{timeout}, nunca crudo |
| T_refusal_not_retryable | P11/C12 | refusal → invalid_request_error/400 no-retryable |
| T_sanitize_no_leak | P12/C13 | Message sin keys/paths/argv, ≤300 |
| T_empty_clientid_fail_closed | P13/C14 | clientID="" → fail-closed no-retryable |

## C) Harness hermético (I3/D6)

- **Stub CLI** (script `#!/bin/sh` en `t.TempDir()`, 0o755): stdout/exit/stderr configurables, copia de stdin a archivo, sleep configurable, contador de spawns simultáneos (markers start/end en lock file, thread-safe).
- **Test-double Backend**: implementa `Backend` (§2.2); `Args` registra `--model`; `TranslateReq` extrae el último user; `TranslateOut`/`TranslateStreamOut` parsean stdout del stub.
- **Identidad de cliente**: inyección de ctx vía `auth.Wrap` (harness HTTP mínimo, patrón e2e_test.go); `clientID=""` sin Wrap para P13. No re-testea auth.
- Cero binario `claude` real, cero suscripción, cero red externa.

## Riesgos de regresión (bajo)

1. **RED por compilación en paquete nuevo** — `internal/subprocess/` no existe; tests referencian tipos inexistentes → fallarían por compile, no por AssertionError (viola el gate). **Decisión del orquestador/owner:** el test-writer crea en RED el **esqueleto de superficie de contrato** (firmas de `Provider`/`Backend`/`Session` transcritas del spec §2 con bodies zero-value), de modo que los tests compilen y fallen por aserción. GREEN rellena la lógica.
2. Inyección de clientID vía auth.Wrap (no re-testea auth).
3. Contador de spawns del stub debe ser thread-safe para no dar falsos positivos en C9.

## Gate — Test Audit

- [x] Spec aprobado (P1-P13, C1-C14, tabla §4.2).
- [x] Comportamiento net-new identificado (paquete nuevo, 0 archivos).
- [x] Contratos compartidos auditados → rely, no re-test.
- [x] 0 tests modificados; paquetes compartidos untouched.
- [x] 19 tests nuevos mapeados, todos de comportamiento observable.
- [x] Sin mocks sobre plumbing; sin duplicación.
- [x] Harness hermético definido.

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
