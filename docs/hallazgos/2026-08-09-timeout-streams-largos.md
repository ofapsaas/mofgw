# Hallazgo: timeout de fallback (120s) corta streams largos de sub-agentes

**Fecha:** 2026-08-09
**Severidad:** ALTA (operativa — causó 3 artifacts de delegación vacíos en el mismo día)
**Estado:** CORREGIDO en producción (config) — bug de diseño documentado pendiente

## Síntoma
Delegaciones de sub-agentes (cdad-test-writer, etc.) volvían con artifact vacío:
- logical-pink-mink (00:12-00:16): 12 steps normales, respuesta final perdida
- expensive-aqua-worm (00:24-00:28): 7 steps normales, respuesta final perdida

El delegado trabajaba normalmente (pasos de lectura/análisis) pero la llamada final
al LLM (la que produce la respuesta al orquestador) moría y el artifact se guardaba
vacío.

## Causa raíz (verificada con evidencia)
`internal/timeouts/timeouts.go:35-40` — `Attempt()` crea `context.WithTimeout(parent, timeout)` que cubre TODA la vida del intento, incluido el streaming completo:

```
21:28:59.793 WARN "stream interrumpido" request_id=9f309fa696c1a33d provider=go-5
            err="provider upstream error: stream read: context deadline exceeded (status 0)"
21:28:59.794 request_end status_code=200 duration_ms=120059  ← 120.059s = EXACTO fallback.timeout
```

El `fallback.timeout: 120s` del config de prod aplicaba a la duración TOTAL del
stream, no solo al TTFB. Un sub-agente que genera una respuesta larga (reportes
RED con ~35 tests descritos) puede tardar >120s en streaming → mofgw cancela el
contexto a los 120s exactos → opencode ve "upstream provider error" → artifact vacío.

## Discrepancia contrato vs implementación
El docstring de `timeouts` (línea 21: "ErrTimeout marca un intento que excedió su
timeout (TTFB)") y la spec 001-006 describen el timeout como TTFB, pero la
implementación de `Attempt()` lo aplica al intento COMPLETO (streaming incluido).
Los requests normales de opencode son cortos y nunca tocan el límite; solo las
respuestas largas de sub-agentes lo pisan.

## Fix aplicado (producción, 09 Ago 2026)
`~/.config/mofgw/config.yaml`: `fallback.timeout: 120s → 300s` (alineado con
`server.write_timeout: 300s`). Backup previo: `config.yaml.bak-20260809-2130*`.
Servicio reiniciado, health OK. La feature de telemetría (009-000) hará estos
cortes visibles de forma sistemática.

## Fix de diseño pendiente (backlog)
Decidir si el timeout de fallback debe ser:
(a) TTFB-only (el stream, una vez arrancado, no tiene deadline de duración), o
(b) TTL de stream configurable separado (p.ej. `stream_timeout`).

La opción (a) es la correcta para el contrato documentado: el timeout existe para
evitar providers colgados en TTFB, no para matar streams de generación larga. La
opción (b) es más segura operativamente (un stream que se queda en loop infinito
no debe colgar el request para siempre).

Registrado por: Ofap (orquestador / dueño del proceso), 09 Ago 2026.
