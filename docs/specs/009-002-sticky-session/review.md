# Review — 009-002-sticky-session

Reviewer model: **mofgw/qwen3.7-plus** (familia distinta al implementer deepseek-v4-flash — anti-confirmation-bias CDAD §2)

Fecha: 2026-08-09 · Rango revisado: `git diff 08f0188..HEAD` (spec + RED + GREEN + fixes de tests + test-audit; 4 commits)

## Resumen de la resolución (orquestador + usuario HITL)

- **Bloqueantes: 0.**
- **Opcionales: 5** (2 Nit, 3 FYI) — 4 aceptados como deuda documentada, **1 aplicado** (Nit #5, documentación del knob en example.yaml).
- Suite completa **326 tests `-race` verdes** (evidencia empírica del orquestador), vet limpio, gofmt limpio (salvo `e2e_009000_test.go`, deuda preexistente en HEAD).

## Hallazgos positivos (verificación de correctness — sin issues)

- **P1/I1 config:** `StickyRoutingConfig{Enabled}` en config.go, default `false`, sin reglas nuevas de validate(), bloque documentado en example.yaml. ✅
- **P2/I3/I4 keying:** `stickyKey = clientID + "|" + sessionID` (proxy.go:444-447), sessionID de `X-Session-Id` solo lectura (L353), clientID del token nunca vacío, sin lectura de `X-Session-Affinity`. ✅
- **P3/I6 AffinityStore:** map + mutex + now inyectable + cap default 100, Set upsert+touch+evict LRU, Get touch, evictLRU bajo lock (sticky.go:32-95). ✅
- **P4/I2 reorder post-filtro:** `resolveReady` primero, `applyStickyReorder` después (router.go:566/574); preferido no en `ready` → slice intacto; log `sticky_applied` solo cuando aplica (L540). ✅
- **P5 frontera primer-byte:** stream pinneado al provider del primer byte (commitStream L759-771); sin cambio post-primer-byte. ✅
- **P6 registro post-éxito:** `Affinity().Set` en `recordCacheTokens` gated por `s.stickyRouting`; fallo total/rechazos pre-router no llegan. ✅
- **P7/I1 transparencia:** sin headers/body/status nuevos; `headerNameSetEqual` (C10) compara sets sticky vs legacy. ✅
- **P8 backward compat:** `Complete`/`Stream`/`New` legacy → helpers compartidos con `stickyKey=""` que retorna inmediato; loop existente literal. ✅
- **P9/I2 no invasivo:** `applyStickyReorder` es el único cambio de flujo; `maxAttempts`/`stateVersion` intactos. ✅
- **Security:** clave computada internamente (token+header), no de input externo; sin valores sensibles logueados. ✅
- **Performance:** reorder O(n) con n ≤ providers (2-5); evictLRU O(cap) solo al exceder; contención de mutex mínima. ✅

## Opcionales (resolución)

### 1. `evictLRU` con flag `first` (sticky.go:85-94) — Nit, aceptado sin cambio
El patrón con `first` es un idiom Go correcto; el early-return alternativo reduciría variables de estado pero no es requerido. Aceptado tal cual.

### 2. `AffinityStore` creado incondicionalmente (router.go:234) — FYI, aceptado
Consistente con `CooldownStore` (también incondicional); costo ~120 bytes + map vacío, nunca consultado con sticky off. Lazy init agregaría nil-checks por beneficio nulo. Aceptado.

### 3. Cláusula de concurrencia sin test dedicado (test-audit §9.2) — FYI, aceptado
Garantía estructural: mutex en Get/Set/Len + `-race` en suite. Análisis del reviewer confirmó que no hay TOCTOU: Get/Set atómicas individualmente; la operación compuesta no requiere atomicidad cruzada (peor caso: ambos requests registran al mismo ganador, resultado correcto). Test dedicado → hardening post-cierre (deuda TECHDEBT).

### 4. `Get` refresca `LastUsed` incluso cuando el preferido no está en `ready` (router.go:530) — FYI, aceptado
Es exactamente la semántica P3 del spec ("Get: refresca LastUsed" sin condicional). Una sesión activa con preferido en cooldown sigue siendo una sesión activa — razonable que no se evicte. Mover el Get post-loop cambiaría la semántica del store respecto del spec. No cambiar.

### 5. `config.example.yaml` no documenta `max_sessions_retained` como knob — Nit, **APLICADO**
Se agrega `max_sessions_retained: 100` comentado en la sección `server:` del example.yaml para que el operador vea la conexión con el tope del sticky (default 100). Commit asociado.

## Veredicto final

**APPROVE** — 0 bloqueantes, 5 opcionales (4 aceptados como deuda, 1 aplicado). Suite 326 verde, vet y gofmt limpios. Los invariantes I1-I7 están verificados. La feature cumple el contrato P1-P9 / C1-C13 del spec aprobado, con backward compat total (P8) verificado por el refactor del loop.

Priorización aprobada por: Ofap (usuario delegado HITL — auto-aprobación GVR, delegación Pablo 08 Ago 2026).
