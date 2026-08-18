# Spec — 015-001-inter-attempt-delay

> Retardo configurable entre intentos de la cadena ante fallos transitorios
> del endpoint compartido (SPOF de cuentas del mismo proveedor).

## Problema

El balanceo mofgw rota entre **cuentas del mismo proveedor** que comparten
`base_url` (ej: 5 cuentas opencode.ai go → mismo endpoint). Cuando el endpoint
cae (blip de red de segundos, caída del proveedor), falla para **todas a la vez**
y el recorrido instantáneo de la cadena no da tiempo a nada:
- connect/network/timeout de una cuenta → se salta a la siguiente en milisegundos
  → mismo fallo → todas caen al instante → sin protección contra el blip.

## Objetivo

Un **knob configurable** que, ante fallos **transitorios** (connect/network/
timeout), inserte un **pequeño delay** antes del siguiente intento, para dar
tiempo al endpoint a recuperarse. Default `0` (cero regresión) + activación
explícita.

## Diseño

1. **Config:** `fallback.inter_attempt_delay` (duration, default `0`).
   - `0` = comportamiento actual exacto (sin delay).
   - `1s`/`2s` = delay entre intentos ante fallos transitorios.

2. **Condicionado al tipo de fallo** — el delay aplica SOLO en fallos
   **transitorios/retryable de red**:
   - Aplica: `timeout`, `network`, `connect`, error I/O, EOF antes de primer byte.
   - NO aplica en `429` (cuota agotada → mover inmediato, la cuenta está exhausta no trabada).

3. **Condicionado al target (familia de endpoint)** — el delay importa entre
   intentos que van a la **misma `base_url`** (SPOF). Entre endpoints distintos
   (GO → qwen → claude-pro) no se retrasa.
   - Implementación: comparar `base_url` del intento anterior vs el candidato.

4. **Ubicación en el código:** en el loop de la cadena del router
   (`internal/router/router.go`, `stream`/`complete`), cuando un intento falla
   con causa transitoria y el siguiente candidato comparte `base_url`, dormir
   `inter_attempt_delay` (ctx-aware: abortar si el ctx del cliente cancela).

5. **Interacción cooldown:** no cambia el cooldown existente (300s). El delay
   es puramente entre intentos dentro del request; el cooldown sigue marcando
   al provider para saltarlo en vueltas posteriores.

## Contrato — postcondiciones (P1-Pn)

- **P1** Config `fallback.inter_attempt_delay` se parsea y valida como duración; default `0`.
- **P2** Con `inter_attempt_delay = 0` (default): el recorrido de la cadena es idéntico al
  actual — SIN delay entre intentos (cero regresión).
- **P3** Con `N>0`: ante un fallo **transitorio** (timeout/network/connect/I-O/EOF pre-byte)
  donde el siguiente candidato **comparte `base_url`** con el intento que falló, se duerme
  `N` antes del intento siguiente (verificable con fake clock).
- **P4** Ante un fallo `429` (cuota agotada): NUNCA se añade delay — salto inmediato al
  siguiente intento.
- **P5** Cuando el siguiente candidato tiene **distinta `base_url`**: SIN delay — salto
  inmediato (GO→qwen→claude-pro no se retrasa).
- **P6** El delay respeta la cancelación del ctx del cliente: si el ctx se cancela durante
  el sleep, el retorno es inmediato (no duerme pasada la cancelación).
- **P7** La suite completa `go test ./... -race` pasa verde; build y vet limpios.
- **P8** La feature es aditiva: no altera el request path por defecto (off por `0`), reversible.

## Criterios de aceptación

1. Config `fallback.inter_attempt_delay` parseada y validada (default 0) → P1.
2. Con `0` (default): comportamiento idéntico al actual (sin delay) — sin regresión → P2.
3. Con `N>0`:
   - Fallo transitorio (timeout/network) + próximo candidato **misma base_url**
     → delay `N` antes del siguiente intento (verificable con fake clock) → P3.
   - Fallo `429` → SIEMPRE inmediato (sin delay) → P4.
   - Candidato de **otra base_url** → SIN delay (salto inmediato) → P5.
   - El delay respeta la cancelación del ctx del cliente (no duerme pasada la cancelación) → P6.
4. Suite completa `go test ./... -race` verde; build + vet limpios → P7.
5. Off por default, reversible, aditivo → P8.

## Estado

**Status: Approved by Ofap (dueño delegado, GVR — Pablo delegó el rol de aprobación HITL, EPIC-009/ADR-004) on 2026-08-17.**

## Anti-scope

- ❌ No tocar la transparencia al cliente (el cliente nunca ve nada).
- ❌ No tocar cooldown / sticky / cache / registro (EPIC-014).
- ❌ No cambiar `max_retries`.

## Config de ejemplo

```yaml
fallback:
  max_retries: 24
  inter_attempt_delay: 1s   # nuevo (default 0 = off)
  cooldown: 300s
```
