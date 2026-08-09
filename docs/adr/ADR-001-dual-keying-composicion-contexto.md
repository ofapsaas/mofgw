# ADR-001: Composición de contexto keyed por sesión (opencode) y por client_id (openclaw/zot) — dual-keying simultáneo

- **Status**: Accepted (implementado en 009-001; draft del scribe aprobado por Ofap, usuario delegado HITL)
- **Date**: 2026-08-09
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — auto-aprobación GVR, 08 Ago 2026). Evidencia: captura de telemetría 009-000 (~103 eventos, `docs/research-context-patterns.md`).
- **Confianza**: Alta (decisión confirmada empíricamente por la captura, no por conjetura; es el criterio de cierre del epic mofgw-009)

## Contexto

El 85% del costo de los requests es cache hit = releer contexto; para optimizar hay que VER la
composición del contexto que los runtimes envían y cómo evoluciona request a request dentro de
una sesión. La investigación previa (estática + captura 009-000, ~103 eventos) confirmó que
solo opencode manda `X-Session-Id` (header, con `X-Session-Affinity`); openclaw/zot (OpenAI/JS,
node) no mandan sesión ni en headers ni en metadata del body (`detected_ids` = 0 en todos los
eventos). Fuerzas: (a) opencode necesita la vista por sesión real (request a request) para
analizar la evolución del contexto; (b) openclaw/zot no tienen fuente de sesión → sin keying
alternativa quedarían sin composición; (c) el endpoint debe ser privado por cliente (auth por
clientID) y la retención acotada (FIFO). La hipótesis de "session id anidado en metadata del
ctx" se descartó con la captura.

## Opciones consideradas

### Opción A: Keying única por `X-Session-Id` (estilo opencode)
- Pros: uniforme; vista request-a-request por sesión para todos.
- Contras: openclaw/zot no mandan sesión → sin data de composición para ellos (pierde el
  análisis del tráfico sin sesión, que es el resto de los runtimes); sin fallback.
- Contras: no hay fuente alternativa (detected_ids = 0) → el keying no se puede "adivinar".

### Opción B: Keying única por `client_id`
- Pros: uniforme; siempre hay client_id (viene del token); simplicidad.
- Contras: sin aislamiento por sesión — opencode pierde la vista request-a-request por sesión,
  justo donde vive el 85% del costo (cache hit por relectura de contexto).

### Opción C: Dual-keying simultáneo (ELEGIDA) — `client|session` cuando hay sesión; `client|` siempre
- Pros: cubre ambos runtimes sin fuente de sesión adicional; el record de cliente es el fallback
  natural (acumula TODO el tráfico del cliente, con y sin sesión); el endpoint expone ambos
  scopes (`scope:"session"` / `scope:"client"`) con la misma shape; alimenta 009-002 (sticky
  routing) para opencode vía sesión y para openclaw/zot vía client_id.
- Contras: dos vistas por request (sesión + cliente) duplican acumulación; consumidores deben
  entender la semántica de scope; el record de cliente nunca se evicta (acotado por cantidad de
  clientes del config).

## Decisión

Composición de contexto con **dual-keying simultáneo**: requests con `X-Session-Id` → records
`client|session`; requests sin sesión (openclaw/zot) → record de cliente `client|` (agregado por
client_id). Ambos se actualizan SIEMPRE cuando hay sesión. Endpoint `GET /v1/context` con
aislamiento por clientID: `?session=<id>` → scope session (404 si no existe); sin session →
scope client. Confirmado por la captura (~103 eventos): el fallback `client|` es el
comportamiento final para runtimes sin sesión.

## Razones

1. **Evidencia empírica:** la captura (103 eventos) confirmó el diseño que el spec marcaba como
   `[KEYING]`; opencode SIEMPRE manda `X-Session-Id`, openclaw/zot nunca (ni header ni metadata,
   `detected_ids` = 0). No hay fuente de sesión alternativa que sumar.
2. **El criterio de cierre del epic era justamente esta decisión** — resolverla con data real en
   vez de conjetura es el objetivo del EPIC-009 (observar antes de diseñar).
3. **El record de cliente (`client|`) es un fallback natural:** siempre existe client_id (auth),
   acumula todo el tráfico del cliente, y da composición a los runtimes sin sesión.
4. **Aislamiento por cliente (I3) y retención acotada (I5):** records por sesión evictados FIFO
   con el mismo tope `max_sessions_retained` (008-003 P4); el record de cliente nunca se evicta.
5. **Privacidad (P7/I1):** el keying no cambia el invariante — solo metadata estructural, nunca
   contenido.

## Consecuencias

**Positivas:**
- opencode tiene composición por sesión real (request a request, history ring buffer);
  openclaw/zot tienen composición agregada por client_id (C15).
- `/v1/context` expone ambos scopes con la misma shape y ceros/vacío nunca nil (P3).
- Base sólida para 009-002-sticky-session (fuente de sesión confirmada por runtime).

**Negativas / trade-offs:**
- El record de cliente duplica parcialmente la data de las sesiones (acumula el mismo request en
  dos records cuando hay sesión) — costo de acumulación doble por request.
- Consumidores del endpoint deben entender `scope` (session vs client); la keying no es
  inferible por runtime desde el payload (solo documentación).
- 404 para clientes sin sesiones (openclaw/zot) es semántica deliberada, consistente con 008-003.

**Neutrales:**
- `stateVersion` 1→2 con `Contexts` en el snapshot: binario nuevo lee v1 sin error; binario viejo
  rechaza v2 con el check existente (aplica el patrón de evolución de TECHDEBT #13 — no requiere
  ADR propio).
- El record de cliente no se persiste sujeta a evicción; los records por sesión se persisten
  acotados al tope vigente.

## Notas

- Fuentes: `docs/research-context-patterns.md` (captura ~103 eventos), spec 009-001 (P2/P3/C14/C15),
  review.md (APPROVE), test-audit.md. Precedente de keying `client|session`: 008-003-stats-por-sesion.
- Si en el futuro un runtime empezara a mandar `X-Session-Id` (p.ej. openclaw), el dual-keying ya
  lo absorbe sin cambio (solo pasaría a tener records de sesión).
- Los ids de sesión de opencode están correlacionados con opencode.log (research-context-patterns.md
  §Correlación validada) — esto habilita el análisis sesión → runtime → composición completo.
