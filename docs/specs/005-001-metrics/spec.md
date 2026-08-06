# Spec — 005-001-metrics: Métricas Prometheus (requests, fallbacks, cooldowns, latencia)

---
feature_id: 005-001-metrics
epic: mofgw-005-ops
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-001-endpoint
paralelizable: no
---

```yaml
context: >
  mofgw es el punto único por el que pasan TODOS los requests de IA del
  servidor (OpenClaw, crons, agentes por usuario). Sin métricas, el
  fallback transparente es una caja negra: no se sabe qué provider
  sirvió cada request, cuántas veces cayó uno, cuánto tiempo pasó un
  provider en cooldown, ni cuál es la latencia real percibida por el
  cliente. La decisión de roadmap es explícita: "metrics primero
  post-MVP" porque lo que no se mide no se mejora (plan.md EPIC-005,
  orden de ejecución). Esta feature expone un endpoint /metrics en
  formato Prometheus con las 4 familias que responden las preguntas
  operativas: requests (volumen y resultado), fallbacks (confiabilidad
  de la cadena), cooldowns (estado de cada provider) y latencia
  (calidad de servicio).
resolution: >
  Endpoint GET /metrics servido por el http.ServeMux existente
  (001-001), handler promhttp.Handler() del cliente oficial
  github.com/prometheus/client_golang (decisión de arquitectura,
  research-architecture.md §librerías). Paquete internal/metrics/
  centraliza la definición de las métricas; los handlers de
  request (001-001), fallback (001-003), retry (002-001) y health
  (002-002) solo llaman a sus helpers — nunca construyen métricas
  inline. Familias:
  1. mofgw_requests_total — counter; labels: path (/v1/chat/completions,
     /v1/models), stream (true/false), status_code, model (pedido por
     el cliente, tal cual llega). Se incrementa UNA vez por request
     finalizado, con el código que recibió el cliente (incluye
     upstream_error 502 de 001-003).
  2. mofgw_upstream_attempts_total — counter; labels: provider
     (id de config), attempt (1..N), result (ok|error). Uno por
     intento real hacia upstream; es la base para calcular tasa de
     error por provider.
  3. mofgw_fallbacks_total — counter; labels: provider (el que
     falló), cause (timeout|http_error|cooldown|network|other —
     clasificación alineada con retryable/no-retryable de 001-003).
     Se incrementa cuando la cadena salta al siguiente provider.
  4. mofgw_cooldown_active — gauge por provider (0|1); 1 mientras
     el provider está en cooldown (001-003). Complemento:
     mofgw_cooldown_total — counter de entradas a cooldown (labels:
     provider, reason).
  5. mofgw_request_duration_seconds — histogram; labels: provider
     (el que sirvió), stream, status_code. Mide latencia TOTAL del
     request vista por el cliente (desde que entra hasta primer byte
     o fin de stream), no la de un intento individual. Buckets
     default del client_golang (0.005..10s) alcanzan para un proxy.
  6. mofgw_provider_healthy — gauge por provider (0|1) alimentada
     por el sondeo de 002-002 (unhealthy = último check falló;
     sin datos = 1 optimista hasta el primer check).
  Se incluyen además las métricas runtime de Go (go_*, process_*)
  que vienen gratis con el registry default del client_golang.
  /metrics NO requiere auth (consistente con /healthz de 002-002):
  el control de exposición es el bind address de la config (por
  defecto 127.0.0.1) o firewall del host; documentado en las notas.
  Cardinalidad acotada: labels solo con valores finitos (path
  canónico, status_code, cause enum, provider = id de config
  existente); nunca labels con cuerpos, keys ni valores de usuario.
postcondition: >
  `curl http://127.0.0.1:3369/metrics` devuelve texto Prometheus
  0.0.4 con las 6 familias + go/process runtime, y los counters
  reflejan exactamente lo ocurrido: tras un request con fallback
  (provider A timeout → provider B 200), se ve
  mofgw_requests_total{status_code="200"} +1,
  mofgw_upstream_attempts_total{A,attempt="1",result="error"} +1
  y {B,attempt="2",result="ok"} +1, mofgw_fallbacks_total{A,cause="timeout"} +1,
  y un sample en mofgw_request_duration_seconds con label provider="B".
  Un provider en cooldown aparece con mofgw_cooldown_active 1 hasta
  que expira. El scrape NO afecta la latencia de los requests
  (handler en goroutine, sin locks en el hot path de streaming).
verifications: >
  - `go vet ./...` y `go test ./internal/metrics/` → 0 failures.
  - Scrape de /metrics parsea como texto Prometheus 0.0.4 (test con
    prometheus client propio: `prometheus/text` parser, o promtool
    si está disponible).
  - Test de integración: request con fallback (mock A siempre
    timeout, B ok) → asserts de las 3 familias + histogram, labels
    exactas como en postcondition.
  - Test de cooldown: con cooldown simulado corto, gauge pasa 0→1→0
    en el timeline esperado.
  - /metrics sin auth responde 200 (mismo handler pattern que
    /healthz); con bind 127.0.0.1 no es alcanzable desde fuera del
    host (netstat: LISTEN solo en loopback).
  - Scrape concurrente (10 goroutines × 50 scrapes) durante un
    stream largo → sin panic ni data race (`go test -race`).
```

## Notas de implementación (para CDAD implementer)

- Paquete `internal/metrics/`: un `Metrics` struct con los vecs
  (CounterVec, HistogramVec, GaugeVec) construidos en `New()` y
  registrados en el registry default. Helpers por evento:
  `RequestDone(path, stream, status, model)`, `Attempt(provider,
  attempt, result)`, `Fallback(provider, cause)`,
  `CooldownStart/End(provider, reason)`, `ProviderHealthy(provider,
  bool)`, `ObserveDuration(provider, stream, status, d)`.
- El handler `/metrics` se registra en el mismo ServeMux de 001-001:
  `mux.Handle("GET /metrics", promhttp.Handler())`. Sin middleware
  de auth; si más adelante se expone la escucha, el operador decide
  (proxy reverse o firewall), documentado en README §operación.
- El histogram de duración se observa en el handler con `time.Since`
  del inicio del request (antes de entrar a la cadena de providers),
  NO por intento — la métrica es "lo que vio el cliente".
- `stream` label: para requests stream, la duración se observa en
  el momento del cierre (fin de stream o error post-primer-byte);
  para no-stream, al completar el body. El counter de requests se
  incrementa en el mismo punto, con el status final.
- Causas de fallback (enum estable, no inventar en runtime):
  `timeout` (TTFB excedido), `http_error` (5xx o 429 no retryable
  según 001-003), `cooldown` (provider ya en cooldown al intentar),
  `network` (error de conexión/DNS/TLS), `other` (catch-all).
- El gauge `mofgw_provider_healthy` lo escribe el ticker de 002-002
  (un solo writer, sin race). Antes del primer check de un provider,
  el gauge no existe (Prometheus lo trata como ausente) — no inicializar
  a 0 para no reportar "down" por defecto.
- Cardinalidad: `model` label toma el valor del body del request
  (model pedido). Si un cliente manda models arbitrarios, la
  cardinalidad crece — aceptable para v1 (es el modelo pedido, dato
  valioso), revisar en EPIC-004 (escala) si hace falta acotar.
- No exponer en labels: IPs de upstream, URLs internas, nombres de
  archivos de config, y por supuesto NUNCA keys ni tokens (regla
  global del proyecto, misma que 005-002-logging).
- La dependencia `github.com/prometheus/client_golang` es la única
  nueva del proyecto (research-architecture.md ya la fija); el resto
  del stack sigue stdlib.
- Relación con el ecosistema: `/metrics` de mofgw es lo que permite
  medir el uso de LLM por agente (los agentes apuntan a mofgw:3369)
  — base para decidir la quota por usuario.

## Criterio de aceptación (checklist CDAD)

- [ ] GET /metrics responde texto Prometheus 0.0.4 (200, sin auth)
- [ ] 6 familias presentes (requests, attempts, fallbacks, cooldown
      active/total, duration histogram, provider_healthy)
- [ ] request con fallback → counters/histogram exactos (test
      integración, labels verificadas)
- [ ] cooldown gauge 0→1→0 en el timeline esperado
- [ ] sin data races bajo scrape concurrente (`go test -race`)
- [ ] sin secrets/URLs internas en labels ni en output
- [ ] bind por defecto loopback; /metrics inalcanzable desde afuera
- [ ] go vet + tests del paquete internal/metrics en verde
