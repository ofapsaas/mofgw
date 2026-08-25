---
epic_id: 017-mofgw-client-hot-reload
epic_name: mofgw-client-hot-reload
created_at: 2026-08-25
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo)
approved_at: 2026-08-25
---

# Epic 017: Hot-reload de clientes/keys (sin reinicio)

## Resumen

Hoy los clientes (id + key_sha256 + budget + embeddings) viven en el `config.yaml` principal, se cargan **una vez al boot** en una estructura inmutable (`auth.byHash`) y validan con **fail-fast** (config inválido → no boot). Cambiar un cliente exige editar el config y **reiniciar**, lo que corta requests en vuelo y es disruptivo para sesiones aisladas.

Este epic extrae el registro de clientes a un **archivo dedicado** y lo hace **hot-reloadable sin reinicio**: habilitar/revocar un cliente y ajustar su budget/embeddings por edición del archivo (o vía un subcomando de operación), con recarga automática por **polling** (TTL configurable, sin dependencias nuevas) y swap atómico del snapshot para no romper la seguridad de concurrencia actual.

## Scope

**In scope:**
- Extraer `clients:` del config principal a un archivo dedicado (p.ej. `clients.yaml`), fuente de verdad única del registro (id, key_sha256, aislamiento, budget, embeddings).
- Motor de recarga: **polling** con TTL configurable (default ~10s), *stat*+lectura solo si cambió, swap atómico del snapshot consumido por `auth`, `limiter`, `embeddings`. **Cero dependencias externas nuevas** (sin fsnotify; respeta el minimalismo del proyecto).
- Semántica de revocación: habilitado/revocado sin reinicio; ventana de gracia **determinística** ≤ TTL (gracia documentada y acotada).
- **Fail-fast en boot** (sin cambios) + **fail-soft en recargas**: archivo malformado en reload → conservar último snapshot bueno, loguear, no tumbar el servicio.
- Subcomandos de operación: `add-client`, `rm-client`, `list-clients`, `validate-clients`; escritura **atómica (tmp+rename)** para que el poller vea siempre un archivo coherente (nunca un estado a medio escribir).
- Preservar invariantes de seguridad: keys nunca en claro en disco, comparación en tiempo constante (`subtle.ConstantTimeCompare`), `-race` limpio, permisos restrictivos del archivo.

**Out of scope:**
- Multi-tenancy (tenants/roles) — invariante de programa.
- Rotación de keys en cascada / DR distribuido.
- Registry compartido entre varias instancias de mofgw (un mofgw por servidor).
- UI/dashboard de clientes.
- Rate-limiting / anti-brute-force — deuda aceptada preexistente (SEC-001), sin cambios.

## Decomposición en features

| # | Feature ID | Descripción (1 línea) | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | `017-001-auth-source-refactor` | Extraer registro de clientes de config.yaml a `clients.yaml` (fuente de verdad), loader único, holder de snapshot atómico; boot fail-fast preservado. Sin hot-reload aún; comportamiento auth idéntico. | — | No |
| 2 | `017-002-hot-reload-engine` | Reloader por poll (TTL configurable, stat+parse), revalidación fail-soft (keep-last-good + log), swap atómico del registry; ventana de revocación determinística ≤ TTL. + ADR-011 (fail-fast→fail-soft). | 001 | No |
| 3 | `017-003-operator-tooling` | Subcomandos `add/rm/list/validate-clients`; escritura atómica tmp+rename; pre-validación (id duplicado, hash 64-hex, reuso de `hash-key`). | 002 | Sí |
| 4 | `017-004-live-client-effects-e2e` | Budget y embeddings hot-reload vía el mismo snapshot (reconciliar `SetBudget` con swap atómico); E2E cross-feature (add→auth ok sin reinicio, rm→401 en ≤ gracia, malformado→keep-last-good, budget aplicado). | 002 | No |

## Contratos cross-feature

El registro de clientes es el contrato compartido entre boot y reloader, y entre `auth`, `limiter` y `embeddings`. Mínimo:

```go
// Fuente de verdad única del registro de clientes (boot + reloader).
type RegistrySource interface {
    Load() ([]config.ClientConfig, error) // reusa la validación actual (fail-fast en boot)
}

// Holder del snapshot del registro, leído por auth/limiter/embeddings.
type Registry struct {
    // mapas derivados: byHash key_sha256->id, byID id->ClientConfig
    // Publicado de forma inmutable y re-punteado atómicamente (atomic.Pointer) en reload.
}
func (r *Registry) Current() *RegistrySnapshot // inmutables; reloader publica uno nuevo
```

- `auth` sigue con su vista mínima `auth.Client{ID, KeySHA256}`; el registry completo (budget/embeddings) vive en `config.ClientConfig` (config.go:182) y es la fuente. `auth` no arrastra budget/embeddings (separación de concerns).
- Usado por: 001 (define tipos), 002 (recarga), 004 (efectos en vivo).

## Criterios de aceptación del epic

- [ ] Las 4 features están done individualmente (spec→RED→GREEN→review→merge).
- [ ] **E2E adición:** cliente nuevo vía `mofgw add-client` → autentica OK **sin reinicio** dentro del poll TTL (verificado con un TTL corto en test).
- [ ] **E2E revocación:** cliente removido → 401 dentro de la ventana documentada; un request en vuelo con el snapshot viejo no falla espurio.
- [ ] **E2E fail-soft:** archivo malformado en recarga → el servicio conserva el último set bueno, loguea el error y sigue up; solo keys genuinamente desconocidas → 401.
- [ ] **E2E budget/embeddings:** edición sin reinicio se refleja en limiter y embeddings.
- [ ] Boot con registry inicial inválido/ausente → fail-fast (comportamiento inalterado).
- [ ] Keys nunca en claro en disco; comparación en tiempo constante preservada; `go test ./... -race` verde; vet + gofmt limpios.

## Riesgos / deuda esperada

- **Riesgo:** cambiar `fail-fast` → `fail-soft` en recargas altera un invariante de seguridad operacional (config malformado ya no tumba el servicio). → **Decisión del epic; se documenta en ADR-011** en 017-002 (no especulativo ahora, aparece cuando se implementa).
- **Riesgo (edición manual):** un editor "a mano" puede dejar el archivo a medio escribir temporalmente. → El poller re-lee hasta ver un archivo válido (fail-soft: conserva el buen snapshot hasta entonces); la vía recomendada es el CLI con escritura atómica tmp+rename (017-003).
- **Deuda:** revocación con gracia ≤ TTL (default 10s), nunca instantánea por diseño (poll-only). Trade-off asumido: simplicidad/cero deps vs latencia de unos segundos. inotify queda como mejora futura (out of scope).
- **Deuda:** `SetBudget` (proxy.go:436) muta `map[string]config.BudgetConfig` en-place → reconciliar con el swap atómico en 017-004 (si queda mutación por compatibilidad, documentar).
- **Seguridad:** el registry es brute-forceable offline si los hashes son de keys débiles → exigir permisos 600 en `clients.yaml` (017-003/004); no cambia el modelo de amenaza de 001-007.

## Stakeholders

- **Aprobador del plan del epic**: Pablo (dueño del proceso mofgw) — HITL **delegado** al orquestador Ofap (pedido explícito de Pablo, sesión 2026-08-25: "actúa como orquestador y dueño del proceso y hitl").
- **Aprobador de specs de features**: Pablo / Ofap (delegado HITL, pedido explícito).
- **Operador del resultado**: Pablo (deploy systemd user, `~/.config/mofgw/`).

## Cambios al plan

- **2026-08-25 — Ajuste del mecanismo de recarga (inotify → poll-only).** Decisión del dueño del proceso (criterio de calidad técnica): message eliminó inotify/fsnotify del alcance. Motivo: (a) `mofgw` evita dependencias de terceros (respecto al minimalismo del proyecto); (b) inotify trae edge cases reales (editores rename-then-write) que el poll resuelve de forma más simple; (c) el pedido original de Pablo fue "releer cada n minutos". Gracia determinística ≤ TTL (default 10s). inotify queda como mejora futura (out of scope). Se actualizaron scope, 017-002, criterios de aceptación y riesgos.

---

Status: **Approved** by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-25