# Spec — 001-005-streaming: Streaming SSE passthrough

---
feature_id: 001-005-streaming
epic: mofgw-001-core
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-001-endpoint, 001-003-fallback
paralelizable: no
---

```yaml
context: >
  Los clientes del proxy (OpenClaw, agentes clientes, crons) usan streaming SSE
  para chat: reciben tokens apenas se generan. El proxy debe pasar el
  stream upstream→cliente SIN buffering total (no puede esperar la
  respuesta completa: destruiría la latencia percibida) y al mismo tiempo
  mantener la transparencia de 001-003: si el provider elegido falla, el
  cliente no debe ver el fallo. El punto crítico es la frontera del
  primer byte: antes del primer evento SSE el request NO está
  comprometido (se puede reintentar con otro provider); después del
  primer byte el stream está comprometido y es imposible retractar
  tokens ya emitidos. Continuations de modelos distintos son
  incompatibles (tokenizers, IDs, estilos): el splice de streams entre
  providers está descartado por inviable. El passthrough debe además
  reescribir el campo `model` (el cliente siempre ve el modelo que
  pidió, no el alias interno del provider) y propagar un id de
  conversación estable.
resolution: >
  El endpoint /v1/chat/completions detecta streaming por el campo
  `stream: true` del body (001-001). Para un request streaming:
  1. Fase pre-primer-byte: el proxy llama Provider.Stream() del provider
     elegido por 001-003 y BUFFERA hasta el primer evento SSE (o el
     primer chunk de datos), con el TTFB de 001-006 como límite. Si el
     intento falla antes del primer byte (429, 5xx, ErrTimeout, error de
     red, 400 por max_tokens), el intento se descarta silenciosamente y
     001-003 prueba el siguiente provider. El cliente solo ve el primer
     intento que produce un primer byte.
  2. Fase post-primer-byte: el stream está comprometido. El proxy
     conmuta a passthrough directo con flush inmediato
     (http.Flusher; FlushInterval -1 si se usa httputil.ReverseProxy),
     reenviando eventos SSE upstream→cliente byte a byte sin inspección
     ni retención. El fallback TERMINA: si el stream muere a mitad
     (upstream corta, timeout de write del server), el proxy emite un
     evento SSE de error bien formado y cierra con el terminador
     `data: [DONE]`. NUNCA reconectar a otro provider post-primer-byte.
  3. Passthrough de headers: se reenvían Accept, Content-Type,
     Accept-Encoding (sin compresión si el cliente no la soporta);
     Authorization se reemplaza por la key del provider elegido. El
     body de respuesta se reescribe en dos campos: `model` → el nombre
     que pidió el cliente; `id` → id estable del proxy (prefijo mofgw-)
     propagado en todos los eventos del stream (o el id upstream si el
     provider lo expone, manteniéndolo consistente entre eventos).
  4. Cancelación: si el cliente se desconecta (context cancelado), se
     cancela el contexto aguas arriba para liberar el stream y los
     recursos del provider.
  5. No-streaming (stream: false o ausente): buffer total, respuesta
     JSON completa; ahí el fallback de 001-003 reintenta hasta agotar
     el chain sin frontera de primer byte (semántica ya cubierta por
     001-003).
  Errores post-primer-byte: evento SSE con `event: error` y
  `data: {"error":{"message":"<causa saneada>","type":"upstream_error"}}`
  (sin keys, URLs internas ni bodies crudos del upstream), seguido de
  `data: [DONE]`. Esto es exactamente lo que 001-003 define como
  "SSE de error bien formado + [DONE]".
postcondition: >
  Un cliente que pide stream:true recibe tokens en cuanto el provider
  los genera, sin buffering total. Si el provider falla antes del primer
  byte, el cliente no ve el fallo (recibe el stream del siguiente
  provider). Si el stream se corta después del primer byte, el cliente
  recibe un SSE de error limpio + [DONE], nunca un stream de otro
  provider, nunca un error crudo del upstream. El campo model de todos
  los eventos coincide con el modelo que pidió el cliente. El id es
  estable a lo largo del stream. Un cliente desconectado libera el
  stream upstream. Tests sin red externa: el comportamiento se verifica
  con fakes HTTP (httptest) y el provider de prueba de 001-003.
verification:
  - go test ./internal/stream/ → 0 failures (sin red externa)
  - httptest: upstream fake SSE que emite 3 eventos con 20ms de gap →
    cliente recibe los 3 eventos en orden, con model reescrito e id estable
  - httptest: upstream que falla 429 sin primer byte → se prueba el
    siguiente provider de la cadena; el cliente ve el stream del segundo
    (reutiliza el harness de 001-003)
  - httptest: upstream emite 1 evento y corta la conexión → el cliente
    recibe evento error + [DONE], sin reconexión
  - httptest: cliente se desconecta a mitad de stream → se verifica que
    el contexto upstream se cancela (fake registra la cancelación)
  - Los eventos SSE se envían con flush inmediato: fake verifica que el
    cliente recibe el primer evento antes de que el upstream emita el
    segundo (latencia percibida, no buffer)
  - go vet ./... limpio
```

## Contrato con features vecinas

| Feature | Acuerdo |
|---------|---------|
| 001-001-endpoint | Endpoint detecta `stream:true`; sirve con Content-Type `text/event-stream`; `GET /v1/models` no aplica |
| 001-003-fallback | Fallback SOLO pre-primer-byte; post-byte → error SSE + [DONE], nunca reconexión. Clasificación retryable compartida (429/5xx/ErrTimeout/red) |
| 001-004-clamp | Clamp de max_tokens se aplica al request ANTES de mandarlo al provider; el stream resultante no se inspecciona |
| 001-006-timeouts | TTFB gobierna fase pre-primer-byte; ServerConfig.WriteTimeout gobierna el stream completo post-primer-byte |

## Notas de implementación

- SSE es HTTP plano (`text/event-stream`): no hace falta librería; `http.Flusher` en el handler.
- Si el primer evento tarda más que TTFB, se cancela el intento y entra el fallback (mismo camino que un error HTTP).
- El buffer pre-primer-byte es liviano: solo retiene el primer chunk hasta decidir el provider; después es passthrough puro.
- Reescribir `model`/`id` en streaming implica parsear línea a línea los eventos SSE (cada evento es un bloque `data:`; los campos de interés van en el JSON del data). La reescritura debe ser streaming-safe: no acumular el stream completo.
- Id estable: si el upstream no expone id, el proxy genera uno (`mofgw-<uuid>`) al primer evento y lo usa en todos los demás.
- Flush: `FlushInterval: -1` (flush tras cada write) si se usa ReverseProxy; con Flusher manual, llamar `Flush()` tras cada evento.
