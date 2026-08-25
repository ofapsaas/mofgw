# Test Audit — 017-001-auth-source-refactor

Feature: `017-001-auth-source-refactor` (epic 017) | Sub-fase: AUDIT | Rol: test-writer (`ethical-azure-slug`)

## 0. Baseline verificado por el orquestador

- `go build ./...` → **Success**
- `go vet ./...` → **No issues found**
- `go test ./... -race` → **697 passed in 27 packages** (concatena P7)

## 1. Cambio de comportamiento que impacta tests

- Se elimina la tag inline `clients:` de `Config`; ya no es válido cargarla solos.
- `clients:` inline sin `clients_file` → error fail-fast de migración (P1).
- `clients` inline + `clients_file` juntos → error (P3, anti dual-source).
- `Config.Clients` permanece (se puebla desde `clients_file`).
- Nuevo `config.LoadClientsFile` + `config.ValidateClients` (extraídas de config.go:600-619).
- Nuevo paquete `internal/clientregistry` (Source, Registry, Snapshot inmutable).
- `auth.Client{ID,KeySHA256}` + `auth.New` **se conservan** → harness de proxy/router/cmd que hacen `auth.New([]auth.Client{...})` NO cambian.

## 2. Tests a MODIFICAR / migrar

Decisión de menor fricción adoptada: **quitar inline `clients:`** de fixtures genéricos (0 clientes = config válido, P1); la cobertura de carga de clientes pasa a los tests nuevos C2/C3. Solo los tests que assertan clientes o validan hash se migran a `clients_file`.

### 2A. Fixture compartida `validYAML` (config_test.go:16-41; `clients:` L38-40) — blast radius 10 tests
Quitar inline clients de `validYAML`; TestLoadValid pasa a migrar a `clients_file`. Afectados:
- TestLoadValid (config_test:53, aserta clients L82) → **migrar a `clients_file`** (único que depende de clientes poblados).
- TestLoadMissingEnv (config_test:94) → quitar inline (evitar enmascarar error env).
- Test014001_P1_DefaultsRegistry (registry_red:17), TestPostcondition8_DefaultsTelemetry (telemetry_red:38), TestPostcondition1_StickyAusenteDefaults (sticky_red:59), TestRED_LoadSinMetadataVacia (metadata_red:63), TestRED_ClientConfig* (clientconfig_conf:32,52,72,99) → quitar inline.

### 2B. Fixtures compartidas
metadataYAML (metadata_red:24), metadata010001YAML (config_test:223), validYAML029 (config_test:295), telemetryYAML (telemetry_red:27), stickyYAML (sticky_red:39) → quitar inline clients.

### 2C. YAML inline en funciones (config_test.go)
- Test029001_ExplicitFirstTokenTimeout (L326), Test029001_ProviderOverride (L382) → quitar inline.
- Test029001_NegativeFirstTokenTimeoutRejected (L353) → quitar inline (masked-error: debe volver el error real por first_token_timeout).
- **TestLoadInvalidClientHash (L178)** → **migrar a `clients_file`** con `key_sha256:"zz"` (preserva intención: validación de hash inválido, P2/C3) — no quitar.

### 2D. registry/telemetry con masked-error
- Test014001_P1_BlockValidoSeCarga (registry_red:43), TestPostcondition8_SinBloqueDefaults (telemetry_red:82-84) → quitar inline.
- **Test014001_P1_EnabledConFileVacio (registry_red:75)** → quitar inline (masked-error: debe volver error por `registry.file:""`).

### 2E. Proxy — único config YAML real
- `internal/proxy/e2e_010001_test.go`: `catalogYAML()` (L50-52, usado por 7 E2E) y TestE2E010001_C2_SinMetadataMinimo YAML (L301-303) → quitar inline clients (harness auth independiente del config YAML).

## 3. Tests a CREAR (RED) — mapeo C→P

| C# | P# | Test nuevo | Capa |
|----|----|-----------|------|
| C1 | P1 | inline `clients:` sin `clients_file` → error de migración | config |
| C2 | P2 | `clients_file` válido → `Config.Clients` poblado; `LoadClientsFile` ok; `ValidateClients` ok | config |
| C3 | P2 | `clients_file` ausente / hash malformado / id vacío / id duplicado / concurrencia negativa → fail-fast | config |
| C4 | P3 | inline + `clients_file` juntos → error dual-source | config |
| C5 | P4 | Snapshot autentica key del registry (200) / desconocida → 401 constant-time | clientregistry |
| C6 | P5 | `SetBudget`/`SetClientEmbeddingsModel`/aislamiento derivados de snapshot.Clients; ausente → sin efecto | E2E proxy + clientregistry |
| C7 | P6 | `Snapshot` inmutable + `ClientByID` + vista auth idéntica a `auth.New`; `-race` limpio | clientregistry |
| C8 | P7 | Gate suite completa 697+ verde, vet/gofmt | suite |
| C9 | P8 | `config.LoadClientsFile` acepta salida de `auth.HashKey`; opcional CLI `runHashKey` | config (+cmd) |

## 4. Riesgos de regresión

- **ALTO blast radius `validYAML`** → mitigado: quitar inline (0 clientes válido), cobertura de clientes pasa a C2/C3.
- **MEDIO masked-error tests** → obligatorio quitar inline en fixtures que esperan error, para no pasar el assert con la razón equivocada.
- **MEDIO catalogYAML cascada** → mitigado (harness auth independiente).
- **BAJO auth/harness** → untouched; gate obliga a que `auth.New`/`auth.Client` queden intactos.

---
Status: **Approved** by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-25