# Test Audit — 018-001-opencode-session-header

Fecha: 2026-09-03 · Baseline: `go test ./...` → **717 pass / 0 fail en 28 paquetes** (verificado empíricamente, salida grep: "717 passed in 28 packages"). `go build ./...` exit 0.

## Tests existentes a modificar

**0 (ninguno).** Análisis:

- **Headers upstream capturados pero jamás aseverados en UA/sesión.** El harness `internal/proxy/e2e_test.go:47` clona headers del upstream (`u.gotHeaders = r.Header.Clone()`) pero los tests existentes solo aseveran `Authorization` (e2e_test.go:41) y headers de respuesta (`X-Mofgw-Cache*` en 010002, `X-Mofgw-Retry-*` en 007003). Ningún test asevera `User-Agent` saliente → **P5 no rompe nada** (el cambio `Go-http-client/1.1` → `mofgw/0.1.0` es invisible a la suite actual).
- **UA en e2e_009000_test.go:415/442** es el UA *entrante* del cliente (telemetría), no el saliente — sin interacción.
- **e2e_008003_test.go** (session affinity 009-002, X-Session-Id entrante) no inspecciona headers salientes — sin interacción.
- **Cache (010002) y singleflight:** la key es method+path+body (proxy.go:262) — no incluye headers → P7 no puede romperlos.
- **Config:** campo nuevo opcional `opencode_session` — los tests de config existentes no enumeran campos exhaustivamente (parseo YAML tolerante) → sin rotura esperada; se valida en C9.

## Tests nuevos (por criterio del spec)

Todos en paquete `internal/proxy` (patrón httptest upstream existente) + `internal/config`:

| Bloque | Test | Criterio | Postcondición |
|---|---|---|---|
| B1 | `TestPostcondition1_SessionHeaderFromInbound` | C1 | P1 |
| B2 | `TestPostcondition2_FallbackClientID` | C2 | P2 |
| B3 | `TestPostcondition3_SanitizeClamp` (subtests: caracteres inválidos, >128, vacío→omitido+warn) | C3 | P3 |
| B4 | `TestPostcondition4_NoLeakWithoutKnob` (2 providers en config: uno con knob, otro sin; assert por-provider) | C4 | P4 |
| B5 | `TestPostcondition5_UserAgent` (subtests: chat, models, embeddings; y ausencia `Go-http-client`/`opencode-cli`) | C5 | P5 |
| B6 | `TestPostcondition6_BodyIntact` (body upstream con knob == body esperado) | C6 | P6 |
| B7 | `TestPostcondition7_CacheKeyStable` (mismo body, distinto X-Session-Id → HIT) | C7 | P7 |
| B8 | `TestPostcondition9_OmitWithWarn` (sin sesión ni client_id → sin header; warn verificable vía logger de test si el harness lo expone, si no: solo ausencia de header) | C8 | P9 |
| B9 | `TestPostcondition10_ConfigWiring` (config con/sin campo; knob llega al provider; I7) | C9 | P10 |

Total: **9 bloques / ~13 tests con subtests.** Cada test referencia su postcondición en nombre/comentario (relevancia 1:1 con el spec).

## Riesgos y notas para RED

1. **Contrato nuevo que el test-writer debe fijar (los tests lo definen, el implementer lo sigue):**
   - `RequestMeta` (D6): transporte de `{SessionID, ClientID}` del handler al provider. Si el nombre difiere en GREEN, es AP-4 → vuelve a test-writer.
   - Constante de versión (D5): valor exacto asertado `mofgw/0.1.0` — si se usa otra, actualizar test en test-writer (no en GREEN).
2. **RED esperado:** los tests B1-B8 fallarán por ausencia del contrato nuevo (compile-error si se referencian símbolos nuevos tipo `RequestMeta`, o AssertionError si se testea solo vía config+headers). Precedente aceptado del proyecto: RED por build-fail en features con contrato nuevo (011-005, 016-002/003/004). **Elección:** tests e2e que solo usan config+headers (sin símbolos nuevos) para que RED sea por AssertionError donde sea posible; B9 (config) por campo ignorado → knob inerte → sin header → AssertionError.
3. **B8 warn:** el harness de test de logging del proyecto — si no hay aserción de warn establecida, el test cubre solo ausencia de header y el warn queda como comportamiento no-aseverado (documentarlo; no inventar mock de plumbing, AP-14).
4. **Embeddings (P11/B5):** el cliente de embeddings arma sus propios requests (internal/embeddings/embeddings.go:68-70) — el test de UA ahí usa el harness de e2e_011006.
5. `go vet` limpio como parte del gate.

## Veredicto

Suite intacta (0 modificaciones), 9 bloques nuevos, mapeo 1:1 con criterios C1-C9. Baseline verde verificado. AUDIT **aprobado para RED**.

Status: **Approved** by Ofap (HITL agent-delegated, pedido explícito de Pablo) on 2026-09-03.
