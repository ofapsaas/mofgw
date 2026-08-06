# Spec — 002-004-degradacion: Degradación controlada si todos los providers caen

---
feature_id: 002-004-degradacion
epic: mofgw-002-resiliencia
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 002-003-absorcion, 001-003-fallback, 002-002-health
paralelizable: no
---

```yaml
context: >
  002-003 garantiza que el error final esté saneado y en formato
  OpenAI-compatible. Pero "todos los providers caídos" no es solo un
  error: es un ESTADO del proxy que puede durar minutos u horas, y
  mientras tanto llegan requests de muchos agentes (crons, heartbeats,
  sesiones). Sin política de degradación, cada request hace el intento
  completo contra la cadena (timeouts incluidos), cada uno con su
  latencia muerta, y el estado del proxy es invisible para el operador
  y para los clientes. Esta feature define el comportamiento del proxy
  en ese estado: (1) detectarlo temprano (con la info de 002-002-health
  y el cooldown de 001-003, no hace falta esperar el timeout de cada
  request para saber que algo está mal), (2) responder rápido y
  coherente (el cliente no espera 5 timeouts seguidos), (3) exponer el
  estado (el operador ve "todos caídos" en /healthz, no tiene que
  adivinar), y (4) degradarse con gracia: una vez que un provider
  vuelve (health check OK o cooldown expirado), el proxy se recupera
  solo, sin intervención. La política de QUÉ responder en degradación
  es conservadora: nunca se inventa una respuesta de éxito (no se
  fabrica contenido LLM fake); se responde el error absorbido con
  estado visible.
resolution: >
  internal/router mantiene un estado agregado `DegradedMode` derivado
  de las señales existentes: (a) health checks de 002-002 marcan
  providers unhealthy; (b) cooldowns de 001-003 marcan providers
  inactivos. Si en el momento de rutear un request NO hay ningún
  provider disponible (todos unhealthy o en cooldown), el router
  entra en fast-fail: NO recorre la cadena esperando timeouts, sino
  que responde de inmediato (target < 100ms) con
  `absorb.Respond(w, ErrAllProvidersDown)` — 502, code
  `all_providers_down`, message genérico ("no providers available").
  Excepciones: (1) si algún provider está en cooldown corto (<= 5s
  restantes), el router espera hasta su expiración (tope 5s) y
  reintenta la cadena una vez antes de responder — evita falsos
  "todos caídos" ante un 429 puntual masivo; (2) los 4xx del request
  del cliente se responden SIEMPRE de inmediato (el error es del
  cliente, no del estado del proxy). El estado agregado se expone en
  /healthz (002-002): 503 con `{"status":"degraded","providers":{...}}`
  cuando todos están caídos, con el detalle por provider (estado,
  cooldown restante, último error saneado). Recuperación: es
  automática y pasiva — cuando un health check marca healthy o un
  cooldown expira, el router vuelve al flujo normal; no hay estado
  "recovering" manual ni reinicio requerido. Un flag opcional
  `degraded_max_requests` (default 0 = ilimitado) permite al operador
  cortar requests en degradación después de N (responde 503 directo)
  para proteger costos cuando un provider con billing rotó a falla
  (la cadena entera caída suele significar problema de red o de keys,
  no de modelos).
postcondition: >
  Con todos los providers caídos, el proxy responde 502
  all_providers_down en < 100ms (sin recorrer la cadena con timeouts),
  /healthz responde 503 degraded con detalle por provider, los logs
  registran cada fast-fail con request_id, y el proxy se recupera solo
  cuando un provider vuelve. Un 429 puntual que deja a todos en
  cooldown corto NO produce falsos all_providers_down (el router
  espera la expiración, tope 5s). El proceso nunca cuelga y nunca
  fabrica respuestas de éxito falsas.
verification:
  - go test ./internal/router/ → 0 failures (sin red externa)
  - test unit: todos los providers en cooldown largo → request
    responde 502 all_providers_down en < 100ms sin llamar a ningún
    provider
  - test unit: providers con cooldown restante <= 5s → el router
    espera la expiración y prueba la cadena de nuevo antes de
    responder
  - test unit: degraded_max_requests=5 → el sexto request en
    degradación responde 503 directo
  - test unit: 4xx de cliente con todos caídos → 4xx de cliente
    (nunca 502)
  - test e2e (httptest): todos los fakes caídos → 502 en < 100ms;
    fake 1 recuperado (health OK) → próximo request fluye normal
  - test e2e: /healthz con todos caídos → 503 degraded, JSON con
    providers detail, sin keys/URLs internas
  - test shutdown: degradación activa + context cancel → sin
    goroutines colgadas (go test -race)
contracts:
  Estado agregado: >
    DegradedMode es derivado, no almacenado: se calcula en cada
    request a partir de healthState (002-002) + cooldowns (001-003).
    No hay mutex nuevo: lee los estados existentes (lecturas bajo el
    mutex compartido del router).
  ErrAllProvidersDown: >
    error centinela en internal/absorb, consumido por Respond():
    StatusCode 502, Code "all_providers_down", Message genérico
    "no providers available" (detalle solo en logs).
  Config: >
    DegradationConfig { MaxRequests int `yaml:"max_requests"`
    (0 = ilimitado), CooldownGrace time.Duration `yaml:"cooldown_grace"`
    (default 5s — espera por cooldowns cortos antes de fast-fail) } —
    anidado en FallbackConfig (001-002).
  Relación con 002-003: >
    El formato del error al cliente lo define 002-003; 002-004 agrega
    la DETECCIÓN temprana (fast-fail) y el estado visible. Ambos
    comparten internal/absorb.
out_of_scope:
  - Respuestas de éxito fabricadas (nunca inventar contenido LLM)
  - Circuit breaker persistente entre reinicios (epímero por diseño,
    igual que cooldowns)
  - Failover a providers de respaldo no configurados (eso es agregar
    providers a la cadena en config, no lógica nueva)
  - Dashboard de estado (EPIC-005 metrics)
```
