# ADR-002: Afinidad sticky como reordenamiento post-filtro (nunca fuerza cooldown/health/cadena)

- **Status**: Accepted (implementado en 009-002-sticky-session; draft del scribe aprobado por Ofap, usuario delegado HITL)
- **Date**: 2026-08-09
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — auto-aprobación GVR, 08 Ago 2026)
- **Confianza**: Media (decisión de diseño ya fijada en el spec 009-002 P4/P9/I2 y verificada por review/test-audit; este ADR la consolida para la memoria de largo plazo — complementa ADR-001, no resuelve una ambigüedad empírica)

## Contexto

El 85% del costo de los requests es cache hit = releer contexto (003-001). La feature 009-002 agrega
afinidad: por clave de sesión/cliente, el provider que GANÓ el último request exitoso se prueba PRIMERO
en el siguiente, maximizando la probabilidad de cache hit. La tensión de diseño: cuánto puede "pesar" la
preferencia sobre la resiliencia existente — cooldown por provider (001-003), reintentos (002-001), health
checks (002-002), orden de cadena. El proyecto tiene un principio rector innegociable (decisión Pablo,
plan.md:19): **el proxy salva todas las situaciones — el cliente nunca se entera de que un provider
falló**. Si la preferencia pudiera saltarse el filtro, un request podría fallar donde la cadena hubiera
respondido, haciendo visible al cliente una falla que sin sticky sería invisible. Eso es inaceptable por
diseño.

## Opciones consideradas

### Opción A: Forzar al preferido (probarlo primero incluso en cooldown/unhealthy)
- Pros: máxima probabilidad de cache hit (el preferido se prueba siempre que la sesión lo eligió).
- Contras: viola la transparencia (un request puede fallar donde la cadena hubiera respondido); pisa la
  política de cooldown/health existente; el fallo del preferido forzado quema intentos y confunde el
  diagnóstico de degradación (002-004).

### Opción B: Reordenamiento post-filtro puro (ELEGIDA) — preferido primero SOLO si ya pasó candidates/resolveReady
- Pros: la corrección y la transparencia se mantienen por construcción (el sticky nunca introduce un fallo
  que la cadena no hubiera tenido); respeta cooldown/health/Serves/cadena sin tocar su lógica; la
  preferencia es un hook local O(n); comportamiento idéntico a legacy en todos los casos "no aplica".
- Contras: el óptimo de cache no siempre se alcanza (preferido en cooldown → primer intento va a otro
  provider, igual que sin sticky); la preferencia es por clave, no por `(clave, modelo)` (D4).

### Opción C: Sticky con umbrales (forzar solo si el preferido está "casi listo", p.ej. cooldown corto)
- Pros: captura parte del óptimo perdido de B.
- Contras: complejidad de estados (qué umbral, quién lo decide); rompe la predictibilidad del
  comportamiento; introduce configuración nueva contra D1 (superficie de config mínima: solo `enabled`);
  el beneficio marginal no justifica el costo.

## Decisión

La afinidad sticky es **solo reordenamiento post-filtro**: `applyStickyReorder` actúa sobre el slice
`ready` devuelto por `resolveReady` (ya post-cooldown/health/Serves, incluido el soft de 002-002),
moviendo el preferido al frente si está presente y preservando el orden relativo del resto. Si el
preferido NO está en `ready` (cooldown, unhealthy con alternativa, ya no sirve el modelo, clave
ausente/evictada/arranque frío, stickyKey vacío) → slice intacto, comportamiento IDÉNTICO a legacy.
Nunca fuerza, nunca salta la cadena, nunca cambia `maxAttempts` ni la clasificación de errores.

## Razones

1. **Transparencia total (principio rector):** el sticky nunca puede hacer visible un fallo que la
   cadena hubiera absorbido; el cliente percibe exactamente la misma respuesta que habría producido el
   provider ganador sin sticky (P7/I1/D6).
2. **Respeta la resiliencia existente sin tocarla:** cooldown (001-003), reintentos (002-001), health
   (002-002) y orden de cadena siguen siendo la autoridad; la preferencia es un hook de UN punto
   (`applyStickyReorder`) entre `resolveReady` y el loop, sin cambios de lógica en el loop.
3. **Corrección por construcción:** nunca se rutea a un provider que no sirve el modelo (`Serves` ya
   filtró); el caso soft de 002-002 queda cubierto (un preferido unhealthy como único candidato ya está
   en `ready` — la preferencia es inofensiva).
4. **Fallo del preferido = costo mínimo:** el loop de fallback actúa normalmente (preferido primero,
   falla retryable → siguiente con cooldown, exactamente como legacy); la preferencia no agrega estados
   al sistema.
5. **Simplicidad y reversibilidad:** la feature es aditiva (`enabled: false` default → cero diferencias
   observables, P8/C11), no invasiva (P9: limiter/budget/ventana/persistencia/telemetría intactos) y el
   reorder es O(n) con n ≤ providers (2-5).

## Consecuencias

**Positivas:**
- Invariante I2 ("preferencia solo post-filtro") verificable y testeado explícitamente (C3 cooldown, C4
  health, C5 Serves — unit + e2e).
- El criterio de aceptación del epic (plan.md:215: "respeta cooldown/health/cadena") queda cubierto por
  diseño, no por accidente.
- La optimización de cache es incremental y no degrada el piso de resiliencia existente.

**Negativas / trade-offs:**
- El óptimo de cache no siempre se alcanza: un preferido en cooldown hace que el primer intento vaya a
  otro provider, igual que sin sticky (la afinidad es probabilística, no garantía).
- Afinidad por clave, no por `(clave, modelo)` (D4): una sesión que alterna modelos servidos por
  providers distintos apunta al último ganador; el óptimo del segundo modelo puede no alcanzarse (la
  corrección se mantiene SIEMPRE vía `Serves`).

**Neutrales:**
- Efímero por diseño (patrón `CooldownStore`): restart → frío; sin persistencia ni bump de `stateVersion`
  (este ADR no documenta evolución de schema).
- Cap del store reutiliza `server.max_sessions_retained` (D4: un solo knob de retención; superficie de
  config mínima `enabled`).
- `X-Session-Affinity` no se usa como fuente (D5/I3): irrelevante porque en opencode es idéntico a
  `X-Session-Id`; sigue solo en la allowlist de telemetría.

## Notas

- Complementa ADR-001 (que fijó el keying `client|session` / `client|`); este ADR fija el CÓMO se aplica
  la preferencia sin romper la resiliencia ni la transparencia.
- Implementado en 009-002-sticky-session; verificado por review (APPROVE, qwen3.7-plus) y test-audit
  (I2 por C3/C4/C5; suite 326 tests `-race` verde).
- Si en el futuro se quisiera "forzar" al preferido (p.ej. para capturar el óptimo en cooldowns cortos),
  este ADR documenta por qué no — y qué habría que re-verificar: transparencia P7/I1 y no-invasividad P9.
