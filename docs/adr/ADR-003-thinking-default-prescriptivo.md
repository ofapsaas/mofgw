# ADR-003: `thinking_default` prescriptivo (el default configurado altera el default nativo del modelo)

- **Status**: Accepted (decisión del dueño del proceso 10 Ago 2026; spec 010-001-catalogo-fiel P7 la codifica; draft del architect, aprobado por Ofap, usuario delegado HITL)
- **Date**: 2026-08-11
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — auto-aprobación GVR, 10 Ago 2026)
- **Confianza**: Alta (decisión explícita del usuario; semántica verificada contra el consumo real de openclaw, research-token-efficiency.md §3/§4 + ref:brainy-white-narwhal)

## Contexto

El catálogo `/v1/models` expone `thinking.default` por modelo (007-002, alimentado por `thinking_default` de la metadata declarativa 007-001). La research §4 documenta el **default nativo** de cada modelo upstream: deepseek-v4-flash effort `high`, glm-5.2 effort `max`, minimax-m3 `adaptive`, kimi siempre-on, qwen thinking ON. La config de producción declaraba valores distintos para dos modelos: deepseek-v4-flash `low` y glm-5.2 `medium`.

La ambigüedad: ¿`thinking.default` describe lo que el modelo hace solo (descriptivo) o lo que el agente debe enviar (prescriptivo)? Si es descriptivo, los valores `low`/`medium` mienten (el modelo piensa en `high`/`max` por default); si es prescriptivo, un runtime que confíe en el campo recibe razonamiento de menor esfuerzo que el nativo — intencionalmente.

## Opciones consideradas

### Opción A: Descriptivo — `thinking_default` = default nativo del modelo
- Pros: el campo refleja la verdad del upstream; sin sorpresas de calidad.
- Contras: el usuario no puede controlar el esfuerzo por defecto de sus agentes sin tocar cada config de cada agente; el ahorro de costo/velocidad no se puede fijar a nivel gateway.

### Opción B: Prescriptivo + enforcement (ELEGIDA) — `thinking_default` = effort que el agente DEBE enviar; el gateway lo inyecta cuando el cliente no especifica
- Pros: el dueño del proceso controla el default efectivo desde un solo lugar (config de mofgw); el catálogo informa exactamente lo que el agente debe mandar; la inyección (feature 010-002) garantiza que el default configurado ALTERE el default nativo del modelo aunque el agente no haga nada (decisión explícita del usuario: "el default sea el configurado, así altere el verdadero default del modelo").
- Contras: el catálogo deja de describir el comportamiento nativo (un observador externo podría esperar `high` de deepseek-v4-flash y ver `low`); requiere documentar la semántica para no confundir consumidores.

### Opción C: Dos campos (descriptivo + prescriptivo)
- Pros: ambas verdades expuestas.
- Contras: superficie de config y catálogo mayor; ningún runtime conocido lee un segundo campo; complejidad sin consumidor real (YAGNI).

## Decisión

**`thinking_default` (y su emisión `thinking.default` en el catálogo) es PRESCRIPTIVO**: documenta el effort de razonamiento que los agentes DEBEN enviar para este modelo, y puede diferir del default nativo del modelo. El gateway lo hace valer inyectándolo en el request cuando el cliente no especifica esfuerzo (feature 010-002-request-path-fiel, decisión de diseño 1.3: inyección per-attempt, provider-aware). El spec 010-001 P7 codifica la emisión; los valores de prod se mantienen (flash low, pro high, minimax adaptive, glm medium, kimi always, qwen high).

## Razones

1. **Decisión explícita del dueño del proceso (10 Ago):** "el default sea el configurado, así altere el verdadero default del modelo" — el control del esfuerzo por defecto vive en la config de mofgw, no en cada agente.
2. **Consistencia catálogo ↔ comportamiento:** el catálogo dice `low` y el gateway inyecta `low` — la información que recibe el agente es exactamente lo que ocurre. Un campo descriptivo + inyección prescriptiva serían contradictorios entre sí.
3. **Economía de config:** un solo knob por modelo (thinking_default) gobierna catálogo + comportamiento efectivo; sin knobs duplicados (C).
4. **Riesgo acotado:** el menor esfuerzo prescriptivo reduce costo/latencia; si el dueño quiere el nativo, cambia el valor en config (flash `high`, glm `max`) y el catálogo y la inyección lo reflejan al reiniciar.

## Consecuencias

- Spec 010-001 P7: `thinking.default` = string configurado, aunque difiera del nativo (testeable).
- Feature 010-002: inyección del default per-attempt (provider-aware; D6: zen pasa `reasoning_effort`; bailian requiere `enable_thinking` para deepseek/glm; kimi nunca — disabled → error).
- `config.example.yaml` y `research-token-efficiency.md` documentan la semántica prescriptiva (los valores nativos quedan en §4 como referencia upstream, no como contrato del catálogo).
- Consumidores del catálogo (OpenClaw lee `thinking.default` para configurar el effort) reciben el valor prescriptivo — comportamiento esperado y deseado por el dueño del proceso.
