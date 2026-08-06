# Spec — SEC-001-security-hardening: fixes de auditoría de seguridad

---
feature_id: SEC-001-security-hardening
epic: mofgw-ops (hardening, post-auditoría)
status: draft (aprobado por Ofap — auto-aprobación GVR, delegación Pablo 06 Ago 2026)
created_at: 2026-08-06
updated_at: 2026-08-06
depends_on: 001-003 (fallback/absorb), 004-002 (limiter X-Agent-Id), 005-005 (logging)
paralelizable: no
---

## Contexto

Auditoría de seguridad externa (Claude, 06 Ago 2026) sobre la versión
publicada de mofgw. Los 5 hallazgos fueron **verificados en el código local**
(orquestador, con file:line). Este spec cubre los 4 que se corrigen + 1 que
se acepta con documentación. Ver `docs/TECHDEBT.md` para el registro.

### Hallazgos verificados

| # | Hallazgo | Evidencia | Severidad |
|---|----------|-----------|-----------|
| 1 | `stream.go:184` — `ev.Err.Error()` crudo al cliente vía SSE (inconsistente con absorb que sanea todo lo demás) | stream.go:184 vs absorb.go:62 | Media |
| 2 | `limiter.go:159-162` — `agentLimits[key]` crece con cada X-Agent-Id distinto, nunca se purga (memory leak lento) | limiter.go:159-162 | Media |
| 3 | `main.go:185-189` — http.Server sin `ReadHeaderTimeout` (Slowloris) | main.go:185-189 | Media |
| 4 | `logging.go:115` — log file con 0644 (legible por cualquier usuario local) | logging.go:115 | Baja |
| 5 | auth sin rate limiting/anti-brute-force | auth.go:73 | Baja — **ACEPTADA** (mitigada por firewall en internet + tailnet confiado + hash 256-bit; documentado, no se corrige en este epic) |

## Postcondiciones

1. **P1 — SSE errors saneados:** el mensaje de error que `stream.Writer.Copy`
   envía al cliente vía SSE cuando el stream se corta (`ev.Err`) pasa por el
   mismo mecanismo de saneamiento que el resto del sistema (absorb.Sanitize).
   El cliente nunca recibe detalle interno del error (hosts, rutas, TLS).
   El detalle completo sigue en logs (como hoy).

2. **P2 — X-Agent-Id acotado:** (a) el header X-Agent-Id se trunca a un
   máximo configurable (default 64 chars) antes de usarse como key del
   limiter; (b) las entradas de agentes inactivos del mapa `agentLimits` se
   limpian (TTL de inactividad, default 10 min) para acotar la cardinalidad.
   El comportamiento con MaxPerAgent=0 (default) no cambia (sin limiter por
   agente → sin mapa).

3. **P3 — ReadHeaderTimeout:** el http.Server configura
   `ReadHeaderTimeout` (default 5s) y `MaxHeaderBytes` (default 1MB — el
   default de Go, explícito para claridad). Mitiga Slowloris.

4. **P4 — Log file 0640:** el archivo de log se crea con permisos 0640
   (owner rw, group r, sin otros) en vez de 0644. Los logs no contienen
   keys (verificado) pero la disciplina de permisos se alinea con el config
   (600).

5. **P5 — Auth sin rate limiting: ACEPTADA** (documentada, no corregida).
   Motivo: proxy interno, firewalld filtra internet (verificado: zona public
   sin puertos), tailnet confiado, hash 256-bit. Se registra en TECHDEBT.

## Invariantes

- I1: Ningún cambio altera el contrato de respuesta en el path feliz
  (transparencia del proxy preservada).
- I2: Los fixes son config-drivable con defaults seguros (no rompen
  configs existentes).
- I3: Suite completa `go test ./... -race` verde (cero red externa).
- I4: Los logs siguen sin filtrar keys ni contenido (regla 005-005).

## Criterios de aceptación

- C1: Stream cortado con error que contiene texto sensible (ej. "dial tcp
  internal.host:443: tls handshake error x") → el cliente recibe mensaje
  genérico sin el detalle; el log conserva el detalle completo.
- C2: X-Agent-Id de >64 chars se trunca (la key del limiter usa ≤64);
  con TTL, una entrada de agente inactivo desaparece del mapa tras el
  período (test con TTL inyectable).
- C3: El http.Server tiene ReadHeaderTimeout y MaxHeaderBytes seteados
  (test de configuración).
- C4: Log file creado con 0640 (test de permisos).
- C5: Suite completa verde, vet y gofmt limpios.

## Cambios de código esperados (orientación)

- `internal/stream/stream.go Copy`: pasar el error por `absorb.Sanitize` o
  el sanitize de provider antes de `WriteError`.
- `internal/limiter/limiter.go`: truncado de agentID en AcquireAgent; TTL
  con limpieza periódica (time.Now + mutex existente).
- `cmd/mofgw/main.go`: ReadHeaderTimeout + MaxHeaderBytes en http.Server.
- `internal/logging/logging.go`: 0640.
- Tests: stream_test.go (C1), limiter_test.go (C2), main_test.go o e2e (C3),
  logging_test.go (C4).

## Fuera de alcance

- Rate limiting por IP en auth (P5 aceptada — ver Contexto).
- Cambios al diseño de auth (hash, constant-time — ya correctos).
