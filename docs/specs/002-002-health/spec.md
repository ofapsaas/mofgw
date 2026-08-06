# Spec — 002-002-health: Health checks periódicos + endpoint /healthz

---
feature_id: 002-002-health
epic: mofgw-002-resiliencia
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-003-fallback, 001-001-endpoint
paralelizable: sí
---

```yaml
context: >
  Los providers se descubren caídos cuando un request real falla (modo
  reactivo de 001-003). Eso funciona, pero tiene dos costos: el primer
  request que pega contra un provider caído paga el timeout TTFB (latencia
  muerta) y el conteo de fallos es ruidoso (un 429 puntual mete al provider
  en cooldown aunque esté sano). Esta feature agrega un sondeo proactivo:
  un health check periódico a cada provider que mide disponibilidad y
  mantiene el estado en memoria. El router usa ese estado para: (1) saltear
  providers marcados como down sin esperar el timeout, (2) no contar los
  fallos del health check contra el cooldown de 001-003 (o contarlos con
  peso propio), y (3) exponer la salud agregada del proxy en un endpoint
  /healthz para el operador y para orquestadores (systemd, monit, uptime
  checks). El health check NUNCA decide por sí solo: es información; la
  decisión de rutear sigue siendo del router de 001-003.
resolution: >
  Un goroutine por provider (o un ticker único que recorre providers, ver
  notas) dispara un request liviano de health — el mismo endpoint que usa
  el provider para su propio health (p.ej. GET {base_url}/models con un
  header de auth mínimo) — cada `HealthConfig.Interval` (default 60s,
  configurable per-provider). Resultado: el estado en memoria es
  `map[providerID]healthState` con { healthy bool, lastCheck time.Time,
  lastLatency time.Duration, consecutiveFailures int }, protegido por el
  mismo mutex del cooldown (o mutex propio). Un fallo del health check
  marca al provider `unhealthy` (no down permanente): el router lo
  saltea en requests nuevos hasta que un check posterior lo marque
  healthy otra vez. La marca unhealthy es SOFT: si el router no tiene
  ningún otro provider disponible, igual intenta el unhealthy (puede
  estar degradado pero responder). El endpoint `GET /healthz` del proxy
  responde 200 con `{"status":"ok","providers":{...}}` si al menos un
  provider está healthy; 503 si todos están unhealthy; siempre incluye
  el detalle por provider (healthy, latency del último check, última
  vez checked) sin API keys ni secretos. /healthz NO requiere auth
  (es para orquestadores); /metrics (EPIC-005) es el que expone
  contadores finos.
postcondition: >
  Cada provider tiene un health check periódico en background con
  resultado en memoria. El router saltea providers unhealthy en requests
  nuevos (salvo que sea el único disponible). /healthz refleja la salud
  agregada y por provider en JSON, con 200/503. Los fallos del health
  check no ensucian el cooldown de 001-003 (el cooldown sigue siendo
  solo para fallos de requests reales). Sin goroutines colgadas: los
  checks se detienen limpiamente al cerrar el server (context de
  shutdown).
verification:
  - go test ./internal/health/ → 0 failures (sin red externa)
  - httptest fake healthy → /healthz 200, provider healthy=true,
    latency > 0
  - httptest fake que devuelve 500 en /models → /healthz 503,
    provider healthy=false
  - fake que recupera (500→200) → próximo check lo marca healthy;
    el router vuelve a usarlo en requests nuevos
  - test router: provider unhealthy + provider healthy → request va
    al healthy sin intentar el unhealthy
  - test router: TODOS unhealthy → request intenta igualmente en
    orden (soft), fallback normal de 001-003 aplica
  - test shutdown: cancelar context → goroutines de health terminan
    en < 1s (go test -race)
contracts:
  Config: >
    HealthConfig { Interval time.Duration `yaml:"interval"`, Timeout
    time.Duration `yaml:"timeout"` (TTFB del check, default 5s) } —
    anidado en FallbackConfig o ProviderConfig (override per-provider).
  Estado compartido: >
    healthState vive en internal/health; el router lo consulta vía
    HealthStatus(providerID) → healthy bool. No comparte mutex con
    cooldown (evita contención entre el ticker y el hot path).
  /healthz: >
    GET /healthz → 200 {"status":"ok","providers":{"provider-a":{"healthy":true,...}}}
    o 503 si todos unhealthy. Sin auth. Formato estable (contrato para
    systemd/uptime checks).
out_of_scope:
  - Health check activo (inyectar requests de prueba a modelos): solo
    GET pasivo del endpoint de health del provider
  - Decisiones de routing basadas en latencia (solo healthy/unhealthy)
  - Persistencia del estado de salud entre reinicios
  - Métricas Prometheus de health (van en EPIC-005; /healthz es el
    contrato simple de operador)
notas_implementacion:
  - Un solo ticker que recorre providers es más simple que N goroutines
    (5 providers → 1 goroutine alcanza, menos superficie de leaks).
    La granularidad de intervalo por provider se respeta con el
    lastCheck de cada entrada.
  - El request de health NO debe contar como request de cliente: no
    toca contadores de fallback ni cooldown.
  - /healthz debe responder rápido (< 100ms) y sin bloqueos de red:
    devuelve el estado en memoria, no hace checks on-demand.
  - En EPIC-001 MVP el endpoint es /healthz (research-architecture
    §2); LiteLLM usa /health/liveliness + /health/readiness (referencia
    §5) — un solo endpoint alcanza para el operador de mofgw.
```

## Referencias cruzadas
- 001-001-endpoint: registro de rutas (http.ServeMux), donde vive /healthz
- 001-003-fallback: decisión de ruteo (el router consume el estado de salud; el health check no decide solo)
- research-architecture §2: `/healthz` y `/metrics` expuestos por el server
- LiteLLM §5: `health_check_interval` para sondear upstreams en background
- 002-004-degradacion: /healthz 503 alimenta la degradación controlada cuando todo está caído
