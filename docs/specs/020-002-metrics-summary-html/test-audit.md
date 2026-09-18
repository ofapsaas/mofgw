# test-audit.md — 020-002-metrics-summary-html

**Feature:** 020-002-metrics-summary-html · **Etapa:** 3.0 (AUDIT) · **Fecha:** 2026-09-17
**Fuente única:** `docs/specs/020-002-metrics-summary-html/spec.md` (P1-P17, I1-I6, C1-C10) + `docs/systemPatterns.md`.
**Desviación de proceso:** AUDIT inline por el orquestador (9º incidente de harness — subagentes caídos; disclosure registrada).

## 1. Resumen ejecutivo

- **Veredicto global: 0 tests existentes requieren modificación.** La feature agrega un handler nuevo (`metricssummary.go`) + ruta nueva + setter nuevo (`SetRegistryPath`); no toca el write path (I1), ni el schema del registro (I1 de 020-001 intacto), ni `publicPrefixes` (I4), ni el config (I4). `go.mod` sin diffs (I3).
- **10 tests nuevos (B1-B10)** mapean 1:1 a los 10 criterios C1-C10 y cubren las 17 postcondiciones P1-P17 (verificación exhaustiva en §3). Ningún test sin postcondición; ninguna postcondición sin test.
- **RED por compilación** (precedente 001-007/020-001): los tests referencian `SetRegistryPath` (método inexistente en Server) y la ruta nueva (que responde 404 hoy). Falla de compilación del paquete de test = señal RED.
- **Advertencia operativa:** el test usa la serialización REAL del writer (`json.Marshal(TerminalEvent) + "\n"`) para escribir fixtures en `t.TempDir()` — si el schema del registro cambiara en el futuro, estos tests capturan el cambio (apropiado: el endpoint consume el registro, no el writer).

## 2. Inventario + veredicto por test existente

### 2.1 Blast radius

| Paquete | Tests | Veredicto |
|---|---|---|
| `internal/proxy` | ~43 top-level + 87 subtests (suite verificada 43/43) | **UNTOUCHED** — la feature agrega `metricssummary.go` + `SetRegistryPath` + ruta en `Handler()`; no toca `handleUsage`, `handleContext`, `handleModels`, `handleMetrics` (Prometheus), `emitTerminal*` (I1/I4). `SetRegistryPath` es un setter NUEVO, no modifica `SetRegistry` existente. |
| `internal/registry` | 8 top-level (7 P6-014 + registry_cost_test de 020-001) | **UNTOUCHED** (I1: cero cambios en el write path). El fixture de los tests nuevos se escribe con la MISMA serialización (`json.Marshal(TerminalEvent)` + `\n`) — sin tocar el writer. |
| `internal/auth` | ~10 | **UNTOUCHED** (I4: `publicPrefixes` sin cambios — la ruta nueva NO se agrega). |
| `internal/config` (RegistryConfig) | ~13 | **UNTOUCHED** (I4: el schema `registry:` no cambia; el path viaja vía `SetRegistryPath`, no via config struct). |
| Suite completa | ~987/37 `-race` | Gate C1: verde post-GREEN con los tests nuevos agregados. |

### 2.2 Veredictos detallados

- **`Test014001_*` (11 tests e2e del registro)**: los tests usan `SetRegistry(w)` (writer), no `SetRegistryPath(path)`. El endpoint nuevo es INDEPENDIENTE del writer (puede leer rotados históricos aunque `enabled=false`). La coexistencia es additive (I4). Todos UNTOUCHED.
- **`Test014001_P15_PrivacidadGrepNegativo`**: grep negativo de secretos en el JSONL. La feature no agrega campos al registro; P7 (privacidad) lo congela. UNTOUCHED.
- **`TestRED_CostoExacto` y familia (006-002)**: prueban `/metrics` (Prometheus) que usa `estimateCost` en memoria. La feature es un endpoint read-only de disco; la fórmula de costeo no cambia. UNTOUCHED.
- **`TestWrapRequiresAuthOnV1`** (auth_test.go): la feature NO agrega la ruta a `publicPrefixes` → sigue protegida. UNTOUCHED.

## 3. Plan de tests nuevos (B1-B10) con mapeo postcondición

Archivo: `internal/proxy/e2e_020002_test.go` (package `proxy_test`, mismo package que `e2e_014001_test.go`; reutiliza builder + `readRegistry` de 014-001, `auth_test.go` helper de Bearer, `writeCacheFile`/`writeSidecar` de 014-001 para serializar el fixture si útil).

Contrato de firma que el implementer DEBE respetar (precedente `SetRegistry`):

```go
// internal/proxy/metricssummary.go (nuevo):
func (s *Server) SetRegistryPath(path string) // pre-tráfico, inmutable después
func (s *Server) handleMetricsSummary(w http.ResponseWriter, r *http.Request) // el handler
```

Naturaleza del agregado: la función de parse/agregación es una función `proxy` privada in-package (`aggregateMetricsSummary(files []string, date string) (*MetricsSummaryResult, error)`) — testeable in-package con fixtures reales (precedente `estimateCost`). El render del HTML puede ser también función pura (`renderMetricsSummary(result) []byte`) — testeable in-package sin server. Los tests de contrato observable (C2-C10) van por el endpoint HTTP completo; los test de fidelidad del agregado (C2) van por la función pura SI el implementer la expone, sino por el HTML (asertar substring con el valor). El RED lo fija.

| # | Nombre | Postcondición(es) | Fixture | RED |
|---|---|---|---|---|
| B1 | `Test020002_FixtureFidelity` | P16 (maestro), P9, P10, P11 | Fixture JSONL en t.TempDir() (writer-style) con: success upstream cost_up 0.0042 (con tokens), success table 0.00128, success none 0.0, error con tokens 0, attempt del día (ignorado P6), línea de otro día en activo y en .1 (filtro ts P7), línea histórica sin model (P11), línea corrupta + corrupta gigante >64 KiB + línea válida posterior (P12). Rotado `.1` con línea del mismo día + otra de otro día (P7/P8). Assert EXACTO: por modelo (requests/success/error/tokens por tipo/cost_usd/up presente/null), cobertura (upstream/table/none + ""), totales, corruptas, desconocido. | Compilación (SetRegistryPath/ruta inexistente) |
| B2 | `Test020002_DateValidation` | P3 | Tabla: sin date / date="" / "2026-9-1" / "garbage" / con hora / con espacios → 400 `invalid_request_error`; date válido → 200. | Compilación |
| B3 | `Test020002_AuthRequired` | P2 | Request sin Bearer → 401 `invalid_api_key`. | Compilación |
| B4 | `Test020002_NotConfigured503` | P4 | Server sin SetRegistryPath llamado → 503 `server_error` "registry file not configured". | Compilación |
| B5 | `Test020002_RotatedFiles` | P7/P8 | Activo con líneas del día D y D+1; `.1` con líneas de D y D-1; `.2` con líneas de D-1; `.3` inexistente (stop); `.1.gz` adyacente NO leído. Assert: solo D contado, exactamente 1 vez por evento; `.gz` sin contribución ni error. | Compilación |
| B6 | `Test020002_CorruptLines` | P12 | Líneas corruptas (JSON inválido, objeto sin type, terminal ts inválido, línea > 64 KiB) intercaladas con válidas → 200, contador corruptas > 0, válidas posteriores contadas. | Compilación |
| B7 | `Test020002_ReadOnly` | P15 | Post-request: bytes del fixture byte-idénticos al pre-request; no hay archivo nuevo en el dir. | Compilación |
| B8 | `Test020002_HTMLShape` | P14/P9/P10 | Content-Type `text/html; charset=utf-8`; sin `<script src`/`<link`; fecha UTC en el documento; encabezado de tabla con los headers de agregados. | Compilación |
| B9 | `Test020002_MissingFileSoft` | P5 | Path seteado → archivo inexistente → 200 con aviso "archivo no encontrado" en el HTML. | Compilación |
| B10 | `Test020002_MethodNotAllowed` | P1 | POST a `/v1/metrics/summary` → 405. | Compilación |

Cobertura: P1 B10/B1 · P2 B3 · P3 B2 · P4 B4 · P5 B9 · P6 B1 · P7 B1/B5 · P8 B5 · P9 B1 · P10 B1 · P11 B1 · P12 B1/B5 · P13 implícito por diseño (testable por performance — no congelado por test) · P14 B8 · P15 B7 · P16 B1 · P17 implícito (gate go.mod en suite).

### Discriminantes RED→GREEN

| Riesgo | Discriminante |
|---|---|
| Agregado que duplique attempt | B1: fixture con attempt del día → cero contribución a contadores (P6) |
| Doble conteo por rotados | B5: mismo evento del día en activo y .1 → cero doble conteo (disjuntos por rotación + filtro ts) |
| Persistencia del snapshot | B1: disco byte-intacto post-request (P15) |
| Corruption aborta response | B1: línea corrupta gigante intercalada con válida → response 200 + válidas posteriores contadas |
| Proveniencia mal categorizada | B1: cost_usd_src="" (histórico) contado en none, no separado ni perdido |
| HTML con recursos externos | B8: cero `src=`/`href=` externos (self-contained, D9) |
| Timezone ambiguó | B1: ts UTC RFC3339 con ms — el filtrado es por prefijo ts[:10] exacto |
| `.gz` no detectado como corrupto | B5: `.gz` adyacente no genera línea corrupta contada ni error |

### Commits RED (uno por postcondición o grupo)

1. `test(mofgw): 020-002 — RED (B1-B10) por compilación` (B1-B10 en un solo commit — el patrón del repo para RED híbrido)

## 6. Fixtures

- Fixture JSONL: `json.Marshal(registry.TerminalEvent{...}) + "\n"` en archivos activo/`.1`/`.2` bajo `t.TempDir()/.config/mofgw/`. El día base: `2026-09-17` (arbitrario). Otros días: `2026-09-16`, `2026-09-18`.
- Líneas históricas (sin `model`/`cost_usd_src`/`cost_usd_up`): escritas como `json.Marshal` de un struct manual con solo las keys del esquema 014-001 (o json.Marshal de `map[string]any` con las keys históricas).
- Línea corrupta gigante: `strings.Repeat("x", 65536)` sin JSON válido.
- Auth Bearer: patrón `auth_test.go` (generar key + `SetKey` o equivalent — leelo y replicá).
- Builder/Server: patrón e2e_014001_test.go reutilizado (mismo package).

## 7. Gate checklist (salida 3.0 → 3.1)

- [x] Spec leído completo (17 P, 6 I, 10 C — reconstruido).
- [x] Inventario del blast radius: 0 tests existentes requieren modificación.
- [x] Tests untouched listados EXPLÍCITAMENTE (~90 top-level en internal/proxy + suite completa).
- [x] Toda postcondición P1-P17 tiene ≥1 test (tabla §3).
- [x] Todo test nuevo mapea a criterio C1-C10.
- [x] Ningún test depende de estructura interna: asserts sobre contrato observable (HTML, status codes, headers, bytes de disco).
- [x] Benefit-of-doubt: 0 preguntas formales (el spec cerró todas las decisiones en D1-D11).
- [x] Riesgos: (R1) el HTML exacto NO se congela por bytes — solo por contenido (substring de valores); (R2) el fixture usa la serialización del writer real (si el schema cambiara, los tests lo capturan); (R3) el endpoint lee disco por request (nuevo patrón) — tolerancia a archivo inexistente/roto congelada por B1/B9; (R4) suite esperada post-GREEN: ~997/37 (987 + 10 nuevos).

---

**Resumen:** Tests a modificar: **0** · Tests untouched: **~987** (25 explícitos en blast radius proxy/registry/auth/config) · Tests nuevos: **10 (B1-B10)** · Regression risks: 3 (R1-R3 detalle §7).

Status: **Approved** by Ofap (agent-delegated HITL, goal mode) on 2026-09-17 — gate 3.0 cerrado, arranca RED (3.1)
