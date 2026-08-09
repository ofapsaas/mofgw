# Spec — 009-002-sticky-session: afinidad de provider por sesión para maximizar cache hit

---
feature_id: 009-002-sticky-session
epic: mofgw-009-contexto-analisis
status: approved
approved_by: Ofap (usuario delegado HITL — auto-aprobación GVR, delegación Pablo 08 Ago 2026)
approved_at: 2026-08-09T17:45:00-03:00
created_at: 2026-08-09
updated_at: 2026-08-09
depends_on: 001-003-fallback (router, cooldown, clasificación), 002-001-retry (loop de intentos), 002-002-health (candidates/soft), 001-007-auth (clientID), 008-003-stats-por-sesion (keying client|session, X-Session-Id solo lectura, max_sessions_retained), 009-000-request-telemetry (fuente de sesión descubierta), 009-001-context-composition (misma fuente de sesión, ADR-001)
paralelizable: no
---

## Descripción funcional

El 85% del costo de los requests es cache hit = releer contexto. El cache de los
providers es por prefijo estable del prompt (003-001): si una sesión manda el MISMO
contexto (con crecimiento incremental) al MISMO provider, ese provider cachea y el
costo baja. Hoy el router recorre la cadena en orden de config en cada request: dos
requests consecutivos de la misma sesión pueden caer en providers distintos (un
provider entró en cooldown, o un fallback rotó la cadena), rompiendo el prefijo de
cache en cada salto. Esta feature agrega **afinidad**: por clave de sesión/cliente,
el provider que GANÓ el último request exitoso se prueba PRIMERO en el siguiente,
maximizando la probabilidad de cache hit.

Es la TERCERA feature del epic 009. Consume la fuente de sesión confirmada por la
telemetría (009-000) y el ADR-001 (dual-keying): opencode manda `X-Session-Id`;
openclaw/zot no → la clave sticky es `client|session` o `client|`. El criterio del
epic (plan.md:215): *"Sticky routing opcional por config (`fallback.sticky_routing`),
transparente, respeta cooldown/health/cadena"*.

La afinidad es **puramente un reordenamiento post-filtro**: `candidates()` ya resolvió
cooldown/health/Serves; si el preferido no está en `ready`, el comportamiento es
idéntico al actual. El cliente nunca percibe el ruteo (transparencia total, D6).

**Limitación aceptada (documentada, no bloqueante):** la afinidad es por clave de
sesión/cliente, NO por `(clave, modelo)`. El mapa guarda un único provider ganador
por clave (D4). Si una sesión alterna modelos servidos por providers distintos, la
preferencia apunta al último ganador; la corrección se mantiene SIEMPRE porque la
preferencia es solo un reordenamiento post-`candidates` (que exige `Serves(model)`)
— nunca se rutea a un provider que no sirve el modelo pedido. El óptimo de cache
para el segundo modelo puede no alcanzarse; extender la clave con el modelo queda
fuera de alcance.

## Contexto técnico

### Modelos/entidades tocadas
- `internal/router/router.go` — `Router` (185-200), `Options` (86-95), `candidates`
  (247-266: post-filtro cooldown/health/Serves + soft de 002-002), `resolveReady`
  (488-506: grace 002-004 + fastFail), `Complete` (510-591: loop con retry/fallback,
  `res.ProviderID` seteado en 554), `Stream` (606-817: `ensureIncludeUsage` 609,
  TTFB, `commitStream` 684-696 — frontera primer-byte), `New` legacy (204),
  `Cooldowns()` (236 — patrón de accessor). NUEVOS: `AffinityStore` (sticky.go,
  patrón `CooldownStore` 109-182), `CompleteFor`/`StreamFor`, accessor `Affinity()`,
  campo `Options.StickyMaxEntries`.
- `internal/proxy/proxy.go` — `handleChat` (297-454): `clientID` (336, del token),
  `sessionID` (339, `r.Header.Get("X-Session-Id")` solo lectura), branch stream
  (427-435: `handleStream` 623-651 llama `router.Stream`) / no-stream (438:
  `router.Complete`); `recordCacheTokens` (489-513): punto único post-éxito con
  providerID conocido para AMBOS paths (432 y 445) — fase 2 de 009-001 ya vive acá.
  Setter patrón `SetContextAnalysis` (223-226).
- `internal/config/config.go` — `FallbackConfig` (78-86), `defaults()` (213-231),
  `validate()` (293-429).
- `cmd/mofgw/main.go` — wiring `router.NewWithOptions` (110-126), setters
  pre-tráfico (184-218, patrón `SetMaxSessionsRetained` en 209-213).
- `config.example.yaml` — bloque `fallback` (18-22).
- `internal/provider/provider.go` — `Provider.ID()` (34), `Serves` (39).
- `internal/metrics/metrics.go` — `SetMaxSessionsRetained` (387-393): knob de tope
  reutilizado; NO se toca (el sticky no extiende `ContextRecord` de 009-001, D3).

### Hooks/extensión disponibles
- `CooldownStore` (router.go:109-182) — patrón de store en memoria con mutex +
  `now` inyectable (fake clock en tests); el `AffinityStore` replica la estructura
  (map + mutex + now inyectable) con evicción LRU por last-used en vez de expiración
  temporal.
- `router.Cooldowns()` (236) — patrón de accessor para exponer el store nuevo.
- `SetMaxSessionsRetained` (metrics.go:387-393) — patrón de setter pre-tráfico del
  tope; el cap del `AffinityStore` reutiliza el MISMO knob del config
  (`server.max_sessions_retained`, default 100) vía `Options.StickyMaxEntries`.
- `recordCacheTokens` (proxy.go:489) — punto único post-éxito con
  clientID/sessionID/providerID para stream y no-stream.
- `logging`/`slog` — `logger.Debug` para el log `sticky_applied` (D6).

### Convenciones aplicables
- Cuerpos crudos `[]byte` (transparencia): el sticky no toca el body.
- Transparencia total (001-003/002-003): el cliente nunca percibe el ruteo.
- Keying `client|session` + FIFO (008-003 P4, ADR-001): misma fuente de sesión que
  009-001 (P2/P3 de su spec).
- X-Session-Id solo lectura, nunca upstream (008-003 I1).
- Stores efímeros en memoria (001-003, 008-003 I4): restart frío, best-effort.
- Fail-fast solo en config; best-effort en runtime (I5 009-000).

## Contrato — firmas

```go
// internal/config — FallbackConfig gana StickyRouting (D1). Scope NO configurable:
// lo decide la fuente automáticamente (ADR-001). Sin otros campos.
type StickyRoutingConfig struct {
    Enabled bool `yaml:"enabled"` // default false
}

type FallbackConfig struct {
    MaxRetries     int               `yaml:"max_retries"`
    Cooldown       time.Duration     `yaml:"cooldown"`
    CooldownJitter time.Duration     `yaml:"cooldown_jitter"`
    Timeout        time.Duration     `yaml:"timeout"`
    Retry          RetryConfig       `yaml:"retry"`
    Health         HealthConfig      `yaml:"health"`
    Degradation    DegradationConfig `yaml:"degradation"`
    StickyRouting  StickyRoutingConfig `yaml:"sticky_routing"` // NUEVO
}

// internal/router — NUEVO store de afinidad (patrón CooldownStore).
// Efímero: restart → frío. Cap = tope de entradas (0 = default 100).
// Evicción LRU por last-used (Set y Get refrescan LastUsed).
type AffinityStore struct { /* map[string]affinityEntry + mutex + now + cap */ }

func NewAffinityStore(now func() time.Time, cap int) *AffinityStore
func (s *AffinityStore) Set(key, providerID string)    // upsert + touch + evict si excede cap
func (s *AffinityStore) Get(key string) (string, bool) // providerID + touch
func (s *AffinityStore) Len() int

// internal/router — NUEVO campo en Options (constructor-time, inmutable).
type Options struct {
    MaxRetries       int
    Cooldown         time.Duration
    CooldownJitter   time.Duration
    GlobalTimeout    time.Duration
    Retry            RetryConfig
    Degradation      DegradationConfig
    Health           *health.Store
    Logger           *slog.Logger
    StickyMaxEntries int // tope del AffinityStore; 0 = default (100)
}

// internal/router — NUEVOS métodos (D2). Legacy Complete/Stream y New intactos.
func (r *Router) Affinity() *AffinityStore // accessor (patrón Cooldowns)
func (r *Router) CompleteFor(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*provider.CompleteResult, error)
func (r *Router) StreamFor(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*StreamResult, error)

// internal/proxy — NUEVO setter pre-tráfico (patrón SetContextAnalysis).
func (s *Server) SetStickyRouting(enabled bool)
// handleChat: si stickyRouting → CompleteFor/StreamFor con key = clientID + "|" + sessionID.
// recordCacheTokens: si stickyRouting → s.router.Affinity().Set(clientID + "|" + sessionID, providerID).

// cmd/mofgw/main.go — wiring (D1/D4).
r := router.NewWithOptions(specs, router.Options{
    // ... existente (110-126) ...
    StickyMaxEntries: cfg.Server.MaxSessionsRetained, // tope reutilizado (008-003)
})
// junto a los setters pre-tráfico (184-218):
srv.SetStickyRouting(cfg.Fallback.StickyRouting.Enabled)
```

## Postcondiciones

1. **P1 — Config `fallback.sticky_routing.enabled` (D1).** `FallbackConfig` gana
   `StickyRouting StickyRoutingConfig` con el único campo `Enabled bool`. Default
   `false` (ausente en YAML → false; cero cambio de comportamiento). `defaults()`
   lo fija explícitamente. `validate()` no agrega reglas (bool sin valores
   inválidos); un valor no-booleano en YAML falla en el unmarshal → error de
   arranque (fail-fast, patrón config). El scope (sesión vs cliente) NO es
   configurable: lo decide la fuente (P2). Wiring: `main.go` llama
   `srv.SetStickyRouting(cfg.Fallback.StickyRouting.Enabled)` antes del tráfico
   (patrón `SetContextMargin`, main.go:206-208). El campo se documenta en
   `config.example.yaml` bajo `fallback:` (18-22).

2. **P2 — Fuente y keying de la clave sticky (D4/ADR-001).** La fuente de sesión es
   `X-Session-Id` (`r.Header.Get("X-Session-Id")`, proxy.go:339 — la MISMA que
   009-001). La clave sticky se computa como:
   - `sessionID != ""` → `stickyKey = clientID + "|" + sessionID` (opencode: afinidad
     por sesión real).
   - `sessionID == ""` → `stickyKey = clientID + "|"` (openclaw/zot: afinidad por
     cliente — un preferido por cliente, comportamiento deseado: maximiza cache hit
     del cliente; confirmado por la captura 009-000/ADR-001: no mandan sesión ni en
     header ni en metadata).
   `clientID` nunca es vacío (viene del token autenticado, 001-007) → la clave nunca
   es `""`. El sticky NUNCA lee `X-Session-Affinity` (D5): es irrelevante porque en
   opencode es igual a `X-Session-Id` (research-context-patterns.md:63-64); sigue
   solo en la allowlist de telemetría (proxy.go:110). `X-Session-Id` sigue siendo
   solo lectura: nunca se forwardea upstream (008-003 I1).

3. **P3 — `AffinityStore` (D3/D4).** Mapa `stickyKey → {ProviderID, LastUsed}` +
   `sync.Mutex` (patrón `CooldownStore`), con `now func() time.Time` inyectable
   (fake clock en tests). Semántica:
   - `Set(key, providerID)`: upsert (crea o reemplaza), `LastUsed = now`. Si
     `Len() > cap` → evicta la entrada con `LastUsed` más viejo (LRU). Nunca
     excede `cap`.
   - `Get(key)`: devuelve `(providerID, true)` si existe y refresca `LastUsed`;
     `("", false)` si no existe.
   - Cap = `Options.StickyMaxEntries`, default 100 (== default de
     `server.max_sessions_retained`). **Criterio (decisión D4 delegada al spec):**
     se REUTILIZA `max_sessions_retained` en vez de un tope propio porque (1) D1
     limita la superficie de config a `enabled`; (2) el espacio de claves es el de
     las sesiones de 008-003 + una `client|` por cliente — ya acotado por
     `max_sessions_retained` y por la cantidad de clientes del config; (3) el
     operador gobierna retención de sesiones y afinidad con el MISMO knob. Las
     claves `client|` cuentan contra el mismo tope: se evictan como cualquier
     entrada por LRU (un cliente activo las refresca en cada request, así que solo
     se evictan cuando están ociosas; la pérdida es benigna — el siguiente request
     re-registra en frío).
   - Efímero: NO se persiste (sin bump de `stateVersion`; restart → frío, el primer
     request de cada clave sigue el orden de config y re-registra al ganador).
     Patrón `CooldownStore`/008-003 I4.
   - Seguro para uso concurrente (mutex).

4. **P4 — `CompleteFor` (D2): reordenamiento post-filtro.** Con `stickyKey != ""`
   y afinidad registrada para `stickyKey` con provider P:
   - `ready` es el slice devuelto por `resolveReady` (ya post-cooldown/health/Serves,
     incluido el soft de 002-002). Si el índice de P está en `ready` → `ready` se
     reordena moviendo el índice de P al frente, preservando el orden relativo del
     resto (el resto de la cadena queda en orden de config).
   - Si P NO está en `ready` → `ready` sin cambios: comportamiento IDÉNTICO a
     `Complete`. Casos incluidos: P en cooldown; P unhealthy con alternativa
     disponible; P ya no sirve el modelo (`Serves` falso); P sin afinidad (clave
     ausente/evictada/arranque frío); `stickyKey == ""`.
   - `ready` vacío (`serveAny` false) → 404 `model_not_found`, idéntico a legacy
     (Complete L515-518).
   - Luego corre el MISMO loop de `Complete` (510-591): retry 002-001 por provider,
     clasificación de errores 001-003, cooldown en fallos retryable, `exhaustedChain`,
     `res.ProviderID` seteado en éxito (554), `resetDegraded`. `maxAttempts` no
     cambia: la preferencia es reordenamiento, no intentos extra. Cuando el reorder
     se aplica, el router emite `logger.Debug("sticky_applied", "sticky_key", key,
     "provider", P, "model", model)` (D6).

5. **P5 — `StreamFor` (D2 + regla de frontera del streaming).** Mismo reorder de
   P4 sobre `ready`, y luego el MISMO loop de `Stream` (606-817):
   `ensureIncludeUsage` (003-001 P2, 609), TTFB, `commitStream` (684-696: el
   primer byte se consume y el stream queda pinneado al provider que lo arrancó).
   - El preferido falla ANTES del primer byte (429/5xx/timeout/red/cierre del
     canal) → fallback al siguiente provider con reintentos 002-001, EXACTAMENTE
     como `Stream` legacy (el reorder solo cambia el orden de prueba).
   - Después del primer byte NO hay fallback (regla 001-003 §B /
     research-architecture.md §3): si el stream muere a mitad → SSE de error bien
     formado + `[DONE]`, nunca reconecta. El preferido no puede cambiarse
     post-primer-byte.
   - El cliente recibe un stream de UN solo provider (nunca mezcla).

6. **P6 — Registro post-éxito (D3).** El proxy registra la afinidad del provider
   GANADOR (el que respondió, no necesariamente el preferido):
   `s.router.Affinity().Set(clientID + "|" + sessionID, providerID)`. Punto de
   registro: `recordCacheTokens` (proxy.go:489) — el punto único post-éxito con
   providerID para stream (432) y no-stream (445) — o inmediatamente tras
   `CompleteFor`/`StreamFor` exitosos (mismo lugar lógico; el contrato exige: se
   registra SOLO cuando un provider respondió o arrancó el stream). Gated por
   `s.stickyRouting` (sticky off → store vacío, cero memoria).
   - Un request SIN éxito upstream (502 all failed, error no-retryable de cliente,
     stream que nunca arrancó) NO actualiza la afinidad: queda la previa (el
     cooldown/health ya cubren al preferido fallido; en el siguiente request, si el
     preferido sigue disponible pero falla de nuevo retryable, la cadena y el
     cooldown actúan normalmente).
   - Requests rechazados ANTES del router (parseo 400, 413, limiter, budget,
     ventana 008-001) NO tocan afinidad.
   - Stream que arrancó (primer byte) y murió a mitad: SÍ registra (el provider
     cargó el contexto en su cache — es el ganador para cache-hit).
   - Best-effort: un error de registro jamás falla el request (I5 009-000).

7. **P7 — Transparencia estricta (D6, plan.md:215).** Con sticky habilitado y
   afinidad aplicada, la respuesta al cliente es IDÉNTICA a la que habría producido
   el provider ganador sin sticky: mismos headers (sin headers nuevos), mismo
   body/SSE, mismos status y errores. La única diferencia es la elección interna de
   provider (más cache hits). Log debug `sticky_applied` (P4) para observabilidad.
   Sin métricas nuevas obligatorias: los contadores existentes (fallback/cooldown/
   usage 005-001/006-001) son suficientes.

8. **P8 — Backward compat total (D2).** `Complete`/`Stream` legacy, `New` legacy
   (router.go:204) y `Options` sin el campo nuevo quedan intactos y funcionales (un
   router sin `StickyMaxEntries` usa cap default y el store no se consulta si el
   proxy no llama a los métodos For). Con `sticky_routing` ausente o
   `enabled: false` (default) el proxy llama `Complete`/`Stream` legacy: cero
   diferencias observables. Todos los e2e existentes (001-003 fallback,
   001-005 streaming, 002-xxx resiliencia, 008-003, 009-000/009-001) pasan sin
   cambios.

9. **P9 — No invasivo + bounds (plan.md:215: "respeta cooldown/health/cadena").**
   La preferencia es SOLO reordenamiento post-`candidates`: nunca fuerza un
   provider en cooldown, unhealthy (salvo soft 002-002: si es el único candidato ya
   está en `ready` y la preferencia es inofensiva), ni que no sirva el modelo; nunca
   salta la cadena ni cambia `maxAttempts` ni la clasificación de errores. No toca:
   limiter, budget, ventana de contexto, clamp, usage, persistencia
   (`stateVersion` intacta), telemetría. Cardinalidad acotada (P3), concurrencia
   segura. Suite completa `go test ./... -race` verde (cero red externa); vet y
   gofmt limpios.

## Invariantes verificables

- I1 — **Transparencia:** el sticky nunca modifica request/respuesta visible al
  cliente (P7).
- I2 — **Preferencia solo post-filtro:** cooldown/health/Serves de `candidates`
  siempre se respetan; el preferido nunca se fuerza (P4/P9).
- I3 — **Fuente de sesión única:** X-Session-Id (ADR-001); X-Session-Affinity
  irrelevante para el sticky (D5, P2).
- I4 — **X-Session-Id solo lectura:** se usa para keying, nunca upstream (008-003
  I1).
- I5 — **Efímero:** la afinidad vive en memoria; restart → frío (patrón
  CooldownStore; P3).
- I6 — **Bounds:** mapa acotado por `max_sessions_retained`, evicción LRU por
  last-used, mutex (P3/P9).
- I7 — **Backward compat:** API legacy + e2e existentes intactos (P8); suite
  `go test ./... -race` verde, vet y gofmt limpios.

## Criterios de aceptación

- C1 (P1): YAML con `fallback.sticky_routing.enabled: true` → parse OK y flag
  true; bloque ausente → false; `enabled: "si"` (no bool) → error de arranque.
- C2 (P2/P4/P6): 2 providers A,B (orden de config A,B) que sirven el modelo, sticky
  on. Request 1 de la sesión s1 (sin afinidad) → A (orden de config); request 2 de
  s1 → A (preferido primero), con 1 solo intento (sin fallback). Afinidad
  `client|s1` → A registrada.
- C3 (P4/P6): afinidad s1 → A; fake clock mete a A en cooldown → request de s1 va a
  B; éxito → afinidad `client|s1` → B (el ganador post-fallback se vuelve el
  preferido); request siguiente → B.
- C4 (P4): afinidad s1 → A; A unhealthy (health store) con B healthy → request de
  s1 → B (preferido no aplica: no está en `ready`).
- C5 (P4): afinidad s1 → A; A deja de servir el modelo (cambia `Models` del fake)
  → request de s1 → B (orden de config; A nunca elegido — no sirve).
- C6 (P2/P4): 2 sesiones s1/s2 del mismo cliente con ganadores distintos (s1 → A,
  s2 → B, p.ej. A en cooldown para el request de s2) → request de s1 → A y request
  de s2 → B (claves independientes, sin contaminación).
- C7 (P2/P4): requests SIN `X-Session-Id` (openclaw/zot) → clave `client|`: 2
  requests → mismo provider preferido (afinidad por cliente). Un request CON sesión
  no usa la afinidad `client|` del cliente ni viceversa.
- C8 (P5): afinidad s1 → A; A devuelve 429 antes del primer byte → stream de s1
  servido por B (fallback pre-primer-byte funcionando con preferencia). Y: stream
  post-primer-byte que muere → SSE de error + `[DONE]`, sin reconectar a otro
  provider (regla heredada, sin cambios).
- C9 (P6): afinidad s1 → A; request de s1 donde TODOS los providers fallan (502) →
  afinidad sigue A (no se actualiza). Request rechazado por limiter/budget →
  afinidad intacta. Stream de s1 que arranca en A y muere a mitad → afinidad
  registrada a A (cache calentado).
- C10 (P7): con sticky on y afinidad aplicada, el body de respuesta/SSE es
  byte-identical al del provider ganador sin sticky; sin headers `X-*` nuevos; el
  log debug contiene `sticky_applied` cuando el reorder se aplicó.
- C11 (P8): sticky off (default) → los requests usan `Complete`/`Stream` legacy
  (mismo orden de config, misma semántica); los e2e existentes (fallback, streaming,
  usage, context) pasan sin cambios.
- C12 (P9/P3): con `max_sessions_retained: 2` y 3 sesiones → la más vieja por
  last-used se evicta; al re-request de esa sesión → sin afinidad (orden de config)
  y se re-registra; `Len() <= cap` siempre. Clave `client|` activa NO se evicta
  mientras se use (Get/Set refrescan LastUsed).
- C13 (P9): suite completa `go test ./... -race` verde (cero red externa); vet y
  gofmt limpios; con requests concurrentes de la misma sesión (sticky on) → sin
  race.

## Cambios de código esperados (orientación, no vinculante)

- `internal/router/sticky.go` (NUEVO): `AffinityStore` (map + mutex + now + cap),
  `affinityEntry{ProviderID, LastUsed}`, `NewAffinityStore(now, cap)`, `Set`/`Get`/
  `Len`, evicción LRU interna bajo lock.
- `internal/router/router.go`: `Options.StickyMaxEntries`; campo `affinity
  *AffinityStore` en `Router`; construcción en `NewWithOptions`; accessor
  `Affinity()` (patrón `Cooldowns()`); `CompleteFor`/`StreamFor` — factorizan el
  loop de `Complete`/`Stream` con un hook de reorder (implementación a elección del
  implementer; el contrato es: reorder post-`resolveReady` + loop existente sin
  cambios de lógica) + log `sticky_applied`.
- `internal/proxy/proxy.go`: campo `stickyRouting bool` + `SetStickyRouting(enabled)`
  (patrón `SetContextAnalysis`); en `handleChat`, branch por flag:
  `CompleteFor`/`StreamFor` con `key := clientID + "|" + sessionID` (sessionID del
  header existente, L339); en `recordCacheTokens`:
  `if s.stickyRouting { s.router.Affinity().Set(clientID+"|"+sessionID, providerID) }`.
- `internal/config/config.go`: `StickyRoutingConfig{Enabled bool}`; campo
  `StickyRouting` en `FallbackConfig`; default en `defaults()`.
- `cmd/mofgw/main.go`: `StickyMaxEntries: cfg.Server.MaxSessionsRetained` en las
  Options (110-126); `srv.SetStickyRouting(cfg.Fallback.StickyRouting.Enabled)`
  junto a los setters (184-218).
- `config.example.yaml`: bloque `sticky_routing` documentado bajo `fallback` (18-22).
- Tests: unit de `AffinityStore` (P3/C12: set/get/evicción LRU/fake clock), unit de
  router con providers fake (P4/P5/C2-C8: reorder, no-aplica, fallback, streaming
  pre-primer-byte), config (P1/C1), e2e `e2e_009002_test.go` (C2-C11, harness de
  e2e_009000/009001).

## Fuera de alcance

- Afinidad por `(clave, modelo)` — el mapa es `stickyKey → providerID` (D4); la
  dimensión modelo se respeta solo vía `Serves` (P4).
- Persistencia de la afinidad entre restarts — efímero, patrón CooldownStore (P3).
- Config de scope por runtime — lo decide la fuente (ADR-001, D1).
- `X-Session-Affinity` como fuente de afinidad — irrelevante (D5, P2).
- Métricas nuevas obligatorias — solo log debug (D6, P7).
- Modificar openclaw/zot para mandar sesión — decisiones de runtime, out of scope
  del epic (plan.md).
- Cache propio en mofgw o "sticky de respuestas" — el cache vive en los providers
  (003-001); el sticky solo maximiza la probabilidad de hit.
- Extensión de `ContextRecord` de 009-001 para guardar providerID — explícitamente
  NO (D3; su spec P2 no guarda providerID).
