---
id: 018-001-opencode-session-header
title: Header x-opencode-session y User-Agent propio en llamadas upstream
status: draft
created: 2026-09-03
motivacion_externa: "Anomaly/OpenCode Go: requests sin x-opencode-session pueden error desde 2026-09-06"
---

# 018-001 — Header `x-opencode-session` y User-Agent propio en llamadas upstream

## Descripción

- **D1 — Requisito del upstream.** Anomaly (OpenCode Go, provider de mofgw) exige en cada request a su ruta Go: (1) header `x-opencode-session` con valor opaco estable por conversación, (2) identificarse con un User-Agent propio ("no broad user agents"), (3) no generar tráfico abusivo. Fuente: opencode.ai/docs/go. Evidencia de falla real hoy: `deepseek-v4-flash` vía ruta Go devuelve HTTP 400 "Model is unavailable" sin el header (hermes-agent #81584/#81832, fix PR #81591).
- **D2 — Causa raíz en mofgw.** mofgw es el "Go HTTP client" que Anomaly observa: `internal/provider/provider.go` arma los requests upstream con solo `Content-Type`, `Authorization`, `Accept` (buildRequest ~L260/295/352). No setea `User-Agent` (Go envía el default `Go-http-client/1.1`) ni ningún header de sesión. `provider.ChatRequest` no transporta session id. La telemetría propia confirma el patrón (docs/research-context-patterns.md): clientes opencode mandan `X-Session-Id` entrante; openclaw/zot no mandan nada.
- **D3 — Solución: knob declarativo por provider.** Nuevo campo `opencode_session: bool` (default `false`) en `ProviderConfig` (internal/config/config.go, L114), mismo patrón que `thinking_path` (L142): declarativo, nunca inferido del ID/base_url. Con el knob activo, mofgw inyecta el header `x-opencode-session` en los requests upstream de ESE provider.
- **D4 — Precedencia del valor.** (a) Header entrante `X-Session-Id` si presente y no vacío; (b) fallback estable: `client_id` del request autenticado (`auth.ClientIDFrom`). Si ambos ausentes/vacíos: omitir el header + warn al log operativo (nombre, nunca valor). El valor se sanitiza (charset `[A-Za-z0-9._-]`, resto reemplazado por `_`) y se clampa a 128 caracteres (precedente prism docs/providers/opencode-go.md).
- **D5 — User-Agent propio, global.** Todos los requests HTTP upstream (chat/completions, GET models, embeddings) envían `User-Agent: mofgw/<version>`. Hoy no existe constante de versión: se define `const Version = "0.1.0"` en un paquete apropiado (ej. `internal/build`), overridable a futuro por ldflags. Prohibido impersonar el CLI: NUNCA `User-Agent: opencode-cli` ni `x-opencode-client: cli` (rechazo comunitario explícito, hermes-agent #81584).
- **D6 — Plumbing mínimo.** El session id y el client_id viajan del handler al provider por un metadato de request (propuesto: struct `RequestMeta{SessionID, ClientID string}` poblado en el handler de chat y accesible por `provider.Client` al armar el request). Los demás endpoints que comparten `provider.Client` (responses) y el cliente de embeddings quedan cubiertos por D5 (UA) pero no inyectan session header salvo knob activo y meta disponible.
- **D7 — Alcance del cambio.** Aditivo. Con config existente (sin el campo nuevo): único cambio observable es el `User-Agent` (D5). Con el knob activo: se agregan los headers de D3/D4. El body upstream nunca se modifica por esta feature.

## Contrato (postcondiciones)

- **P1 — Header con sesión real.** Un request a /v1/chat/completions con `X-Session-Id: ses_abc` dirigido a un provider con `opencode_session: true` produce un request upstream con header `x-opencode-session: ses_abc`.
- **P2 — Fallback client_id.** El mismo request SIN `X-Session-Id` produce upstream `x-opencode-session: <client_id>` (sanitizado/clampeado).
- **P3 — Sanitización.** Un valor con caracteres fuera de `[A-Za-z0-9._-]` o mayor a 128 chars produce un header sanitizado (caracteres reemplazados por `_`, longitud ≤ 128). Un valor que tras sanitizar queda vacío → header omitido + warn.
- **P4 — No leak.** Un request dirigido a un provider con `opencode_session: false` (default) produce un request upstream SIN `x-opencode-session`, aunque el cliente mande `X-Session-Id`.
- **P5 — User-Agent.** Todo request upstream HTTP (chat, GET models, embeddings) lleva `User-Agent: mofgw/0.1.0` (valor exacto de la constante). Ningún request upstream lleva `Go-http-client` ni `opencode-cli`.
- **P6 — Body intacto.** El body upstream es byte-idéntico al que se enviaría sin esta feature (misma re-encode que hoy si aplica; la feature solo agrega headers).
- **P7 — Cache estable.** La key de response cache/singleflight no depende del header inyectado: dos requests con mismo body y distinto `X-Session-Id` al provider con knob comparten cache exact-match igual que hoy.
- **P8 — Telemetría intacta.** El evento `request_telemetry` entrante no cambia (allowlist y comportamiento actuales); el header inyectado es saliente y no se registra.
- **P9 — Omisión con warn.** Con knob activo y sin sesión ni client_id → request upstream SIN header + log warn operativo (sin valor).
- **P10 — Config.** `opencode_session` parsea como bool opcional; default false; config existente carga sin cambios (I7). (Verificación de parseo/wiring en test de config.)
- **P11 — Cobertura de endpoints.** POST /v1/chat/completions, POST /v1/responses y GET /v1/models (vía provider.Client) cumplen P1-P5 según corresponda; embeddings cumple P5 (UA) y no inyecta session header (su cliente no recibe meta de sesión).

## Invariantes

- **I1 — Declarativo, nunca inferido.** El knob se declara por provider en config; jamás se deduce del ID, base_url o modelo (consistente con I2 de 010-002).
- **I2 — No leak de session ids.** `x-opencode-session` (y cualquier derivado de `X-Session-Id`/client_id) solo sale hacia providers con knob activo.
- **I3 — Headers only.** La feature no muta bodies upstream.
- **I4 — Sin impersonación.** Nunca se envían `User-Agent: opencode-cli`, `x-opencode-client: cli`, ni combinaciones que simulen ser el CLI oficial.
- **I5 — Estabilidad del valor.** El valor no es aleatorio por request ni depende de contenido rotante (hash de mensajes prohibido): misma conversación → mismo valor; mismo cliente sin sesión → client_id.
- **I6 — Stateless.** mofgw no incorpora estado nuevo (sin TTL, sin mapas) para derivar el valor.
- **I7 — Backward compat de config.** Configs existentes cargan idéntico; el campo nuevo es opcional con default false.

## Criterios de aceptación (con mapeo test)

| # | Criterio | Test |
|---|----------|------|
| C1 | P1: upstream recibe `x-opencode-session == X-Session-Id` entrante | `TestPostcondition1_SessionHeaderFromInbound` (e2e proxy + httptest upstream que captura headers) |
| C2 | P2: fallback a client_id sanitizado | `TestPostcondition2_FallbackClientID` |
| C3 | P3: sanitización y clamp (incluye caso vacío→omitido+warn) | `TestPostcondition3_SanitizeClamp` |
| C4 | P4: provider sin knob → sin header (2 providers: uno con, otro sin; assert por-provider) | `TestPostcondition4_NoLeakWithoutKnob` |
| C5 | P5: UA `mofgw/0.1.0` en chat, models y embeddings; y ausencia de `Go-http-client`/`opencode-cli` | `TestPostcondition5_UserAgent` |
| C6 | P6: body upstream idéntico con knob activo vs. snapshot esperado | `TestPostcondition6_BodyIntact` |
| C7 | P7: cache hit entre requests con distinto X-Session-Id (mismo body) | `TestPostcondition7_CacheKeyStable` |
| C8 | P9: omisión con warn cuando no hay valor | `TestPostcondition9_OmitWithWarn` |
| C9 | P10/I7: config con y sin campo carga; knob activo llega al provider | `TestPostcondition10_ConfigWiring` |
| C10 | Suite completa verde (`go test ./... -race`), vet limpio | baseline + GREEN |

## Fuera de alcance

- Parchar harnesses (openclaw/zot) para que serialicen su session id — mejora posterior; el fallback client_id la hace innecesaria para el deadline.
- Sesión real por conversación para clientes no-opencode (requiere cambios en los harnesses o el diseño de inyección desestimado).
- Ajustes por la respuesta de Anomaly (formato exacto del valor, comportamiento post-09/06) — el diseño con valor opaco es robusto a las variantes observadas; si Anomaly pide algo distinto, se revisa P3/D4 en un patch.
- Cambios a la telemetría (009-000) ni al keying de composición (009-001).
- Impersonación de cabeceras del CLI oficial (prohibida, I4).

## Alternativas desestimadas

- **Hash del historial de mensajes como session id:** inestable ante compaction/rotación de contexto → rotación de "sesión" a mitad de conversación → cache miss + routing sticky roto (sticky por valor del header: anomalyco/opencode #46011; hashing desestimado por la comunidad: anomalyco/opencode #21630).
- **ID generado y cacheado por mofgw (client_id → uuid con TTL):** introduce estado en mofgw (hoy stateless salvo response cache), rotación silenciosa al expirar, complejidad de concurrencia/tests para un beneficio solo de eficiencia. Viola I6.
- **Inyectar el header en todos los providers:** leak de session ids a terceros (viola I2; precedente OmniRoute #4022 lo restringe a upstreams opencode-family).
- **Parchar openclaw/zot:** frágil con N clientes y no controla el deadline; queda fuera de alcance.

## Estado

Status: **Draft** — Pendiente aprobación del usuario (Pablo/Ofap HITL — indelegable).
