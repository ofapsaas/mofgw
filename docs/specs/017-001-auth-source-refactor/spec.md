# Spec — 017-001-auth-source-refactor: extracción del registro de clientes a archivo dedicado

---
feature_id: 017-001-auth-source-refactor
epic: mofgw-017-mofgw-client-hot-reload
status: approved
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo)
approved_at: 2026-08-25
created_at: 2026-08-25
depends_on: 001-002-config, 001-007-auth
paralelizable: no
---

## 1. Descripción

Hoy el registro de clientes (id + key_sha256 + max_concurrent* + budget + embeddings) vive como el bloque inline `clients:` DENTRO del `config.yaml`, se carga **una vez al boot** y valida con **fail-fast** (config.go:600-619). Es consumido en producción por `auth` (main.go:166-170), el `keyed limiter` de aislamiento (194-204), `srv.SetBudget` (262-268) y `srv.SetClientEmbeddingsModel` (314-318).

El epic 017 agrega **hot-reload sin reinicio** de ese registro. Esta feature (`017-001`) entrega **solo la refactorización estructural que lo habilita**:

1. **SINGLE source (decisión P1):** el registro de clientes sale del `config.yaml` a un archivo dedicado referenciado por `clients_file:`. Se **elimina** el bloque inline `clients:`. Una sola fuente de verdad → 017-002 (reloader) recarga un único archivo sin ambigüedad.
2. **Holder de snapshot** (`internal/clientregistry.Registry` + `Snapshot` inmutable) como punto único de acceso para los consumidores, aunque por ahora se construya una sola vez al boot (P3-(a)) → 017-002 solo agrega el poller + re-punteo atómico, sin volver a tocar `main.go`.
3. **Validación reutilizable** (`config.ValidateClients` extraída de config.go:600-619) compartida por el `Load()` de boot (fail-fast) y por el futuro reloader de 017-002 (fail-soft).

**Fuera de alcance (anti-scope):**
- ❌ Hot-reload / poller / swap atómico / fail-soft en recarga → **017-002**.
- ❌ Aplicación en vivo de budget/embeddings (re-llamada de `Set*` en recarga) → **017-004**.
- ❌ Subcomandos `add/rm/list/validate-clients` → **017-003**.
- ❌ Cambiar el modelo de seguridad (constant-time, keys hasheadas) — se preserva tal cual.
- ❌ Multi-tenancy, rate-limiting (deuda aceptada preexistente).

## 2. Contrato

### 2.1 Config — knob `clients_file` (SINGLE source)

`Config` gana un campo top-level (decisión P2):

```go
// ClientsFile: path al archivo de registro de clientes (única fuente).
// Se usa tal cual (CWD), igual que server.state_file/registry.file/telemetry.file.
// Ende 017-001, la tag yaml `clients` inline se ELIMINA de Config.
ClientsFile string `yaml:"clients_file"`
```

Semántica de carga (boot, fail-fast — P1/P2/P3):
- `clients_file` presente → se carga y valida el archivo; es la ÚNICA fuente.
- `clients_file` ausente → sin clientes (equivalente a hoy sin bloque `clients:`); si además el YAML trae un bloque inline `clients:` → **error fail-fast de migración** ("clients inline ya no se soporta; mové el bloque a `clients_file`").
- Archivo ausente/inválido → error fail-fast (igual semántica que hoy).

`config.LoadClientsFile(path string) ([]ClientConfig, error)` — lee + valida reusando `config.ValidateClients([]ClientConfig) error` (extraída de config.go:600-619, misma lógica: id obligatorio, ids únicos, key_sha256 len64+hex, concurrencia no negativa).

### 2.2 Paquete nuevo `internal/clientregistry` — holder de snapshot

Contrato cross-feature del epic (plan.md:47-63), materializado. **No colisiona** con `internal/registry` (accounting 014-001).

```go
package clientregistry

// Source lee el registro desde el archivo (boot fail-fast; 017-002 lo
// re-usa para fail-soft).
type Source struct {
    Path string
}
func (s *Source) Load() ([]config.ClientConfig, error) // usa config.LoadClientsFile

// Snapshot es inmutable; los consumidores lo leen. 017-002 lo re-puntea
// atómicamente (atomic.Pointer) sin romper la garantía de no-mutación.
type Snapshot struct {
    Clients       []config.ClientConfig   // registro completo
    ClientsByID   map[string]config.ClientConfig
    authByHash    map[string]string       // key_sha256 lowercased -> id
}
func (s *Snapshot) Authenticate(r *http.Request) (string, error) // vista auth idéntica a la actual
func (s *Snapshot) ClientByID(id string) (config.ClientConfig, bool)

// Registry es el holder. 017-001: se construye una vez al boot.
// 017-002: agrega Reload()/poller que construye un Snapshot nuevo y lo publica.
type Registry struct {
    current atomic.Pointer[Snapshot]
    logger  *slog.Logger
}
func New(src *Source, logger *slog.Logger) (*Registry, error) // carga inicial; fail-fast
func (r *Registry) Current() *Snapshot                        // Snapshot inmutable
```

GARANTÍA DE CONCURRENCIA: el `Snapshot` se publica inmutable; nadie lo muta después de construirlo. El `auth.ConstantTimeCompare` se preserva (misma primitiva que `auth.go:84-88`). `auth.Client{ID, KeySHA256}` (vista mínima) se conserva y se deriva de `Snapshot` para no acoplar auth al registro completo.

### 2.3 main.go — consumidores leen de `Registry.Current()` (P3-(a))

`run()` construye `clientregistry.New(source, logger)` una vez al boot. `auth.New` (main.go:166-170) es reemplazado por el `Snapshot.Authenticator()`/`Authenticate` del snapshot; los budgets del `keyed limiter` (194-204), `srv.SetBudget` (262-268) y `srv.SetClientEmbeddingsModel` (314-318) se derivan de `snapshot.Clients`. Comportamiento observable idéntico.

## 3. Postcondiciones

- **P1** — El registro de clientes se carga SOLO de `clients_file`. Un config con bloque inline `clients:` **sin** `clients_file` → error al cargar (fail-fast), con mensaje claro de migración.
- **P2** — Con `clients_file` referenciado, el archivo se carga y valida al boot con la semántica actual de fail-fast: id obligatorio, ids únicos, `key_sha256` de 64 chars hex, concurrencia no negativa. Archivo ausente o inválido → `config` NO arranca.
- **P3** — Config con `clients_file` Y bloque inline `clients:` a la vez → error fail-fast (SINGLE; anti dual-source).
- **P4** — Comportamiento de auth idéntico: una key cuyo hash esté en `clients_file` → request autentica; key desconocida o ausente → `401 invalid_api_key` (comparación en tiempo constante).
- **P5** — Los efectos por cliente cargados del archivo (budget, embeddings, límites de aislamiento) se aplican igual que hoy cuando el cliente está en el registry; cliente ausente del registry → sin budget/embeddings/aislamiento (igual que hoy sin `clients:`).
- **P6** — `Registry.Current()` devuelve un `Snapshot` inmutable; su vista de auth (hash→id) es funcionalmente idéntica a la de `auth.New` actual.
- **P7** — No hay cambio observable: suite completa `go test ./... -race` verde; `go vet` y `gofmt` limpios; los 697 tests de línea base pasan (con ajuste de fixtures solo donde el YAML inline `clients:` se migra a `clients_file`).
- **P8** — `mofgw hash-key` sigue funcionando sin cambios (su output es aceptado por la validación de `clients_file`).

## 4. Criterios de aceptación (mapeo test)

| C# | Criterio | Postcond. | Cómo se verifica |
|----|----------|-----------|------------------|
| C1 | Config con `clients:` inline sin `clients_file` → error de carga | P1 | Test unit config: `Load`/`Parse` YAML con `clients:` y sin `clients_file` → error; mensaje menciona migración |
| C2 | `clients_file` con registro válido → se carga y `Config.Clients` poblado | P2 | Test unit config: `Load` con `clients_file` apuntando a YAML válido → `len(Clients)>0` |
| C3 | `clients_file` ausente/inválido → error fail-fast | P2 | Test unit config: path inexistente/`key_sha256` malformado → error |
| C4 | ambos inline + `clients_file` → error | P3 | Test unit config: YAML con ambos → error |
| C5 | Key del registry autentica; desconocida → 401 (constant-time) | P4 | Test E2E proxy: Bearer válido → handler; Bearer desconocido → 401 `invalid_api_key` |
| C6 | Budget/embeddings/aislamiento derivados del archivo | P5 | Test E2E proxy: `SetBudget`/`SetClientEmbeddingsModel` obtenidos de `snapshot.Clients`; cliente ausente → sin efecto |
| C7 | `Snapshot` inmutable + vista auth idéntica | P6 | Test unit clientregistry: `Snapshot` expone vista auth; mutación posterior no lo altera; `go test -race` limpio |
| C8 | Suite completa verde | P7 | `go test ./... -race` 697+ verdes; `go vet`, `gofmt` |
| C9 | `mofgw hash-key` inalterado | P8 | Test CLI `runHashKey` + `config.LoadClientsFile` acepta su salida |

## 5. Decisions registradas (epic)

- **D1 (P1)** — SINGLE source: se elimina `clients:` inline; `clients_file` única fuente.
- **D2 (P2)** — `clients_file` top-level; path as-is (CWD), consistente con state_file/registry.file/telemetry.file.
- **D3** — Paquete nuevo `internal/clientregistry` (evita colisión con `internal/registry` 014-001); `auth` conserva vista mínima.
- **D4** — `config.ValidateClients` extraída para reuso boot (fail-fast) / reloader (fail-soft en 017-002).
- **D5 (P3)** — Cablear ya los consumidores a `Registry.Current()` en 017-001 para que 017-002 solo agregue poller+re-punteo.

---
Status: **Approved** by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-25