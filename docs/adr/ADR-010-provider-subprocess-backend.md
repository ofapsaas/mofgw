# ADR-010: Provider `subprocess` — motor genérico que ejecuta el CLI de un backend de IA como subproceso

- **Status**: Accepted (feature 013-001-subprocess-core, primera del epic 013-mofgw-cli-subprocess)
- **Date**: 2026-08-12
- **Deciders**: Ofap (dueño del proceso delegado por Pablo — HITL, 12 Ago 2026)
- **Confianza**: Alta (decisión fundacional del epic: codificada en el spec D1-D9/I1-I6, implementada en `internal/subprocess`, verificada empíricamente por la suite completa 531/20 `-race` y por la review — 0 bloqueantes)

## Contexto

mofgw es un proxy de IA OpenAI-compatible con fallback transparente entre providers HTTP. El epic 013 busca agregar un provider que use la **suscripción Claude Pro** como fallback, ejecutando el CLI oficial de Anthropic (`claude`) como subproceso en lugar de hablar HTTP con un endpoint. Motivación: es la **vía bendecida** por Anthropic (no van a bloquear su propio binario), y evita el reverse-engineering de OAuth y los bridges HTTP hacia `api.anthropic.com` (alternativas descartadas en el plan del epic).

El patrón no es exclusivo de Claude: hay otras suscripciones de CLIs de IA con la misma característica. Se generaliza a un motor `subprocess` agnóstico con adapters por backend (claude hoy; gemini/codex a futuro cuando estén disponibles).

## Opciones consideradas

### Opción A: Bridge HTTP hacia `api.anthropic.com` (DESCARTADA)
Pros: flujo familiar HTTP. Contras: requiere reverse-engineering de OAuth de la suscripción (no hay API key para Claude Pro de consumidor), mantenimiento frágil, y Anthropic podría bloquearlo. Descartada en el plan del epic.

### Opción B: Adapter solo-claude acoplado (`type: claude`)
Pros: simple para el caso único. Contras: hardcodea strings/argv de claude en el motor; cada backend futuro (gemini, codex) duplicaría exec/serialización/errores; el motor conocería formatos backend-específicos (viola el principio de transparencia del proxy).

### Opción C: Motor genérico + interfaz `Backend` — ELEGIDA
El motor (`internal/subprocess`) posee exec, resolución de sesión por cliente, serialización, captura de stdout/exit, fabricación de usage y normalización de errores → `provider.ErrUpstream`. El `Backend` posee argv, traducción de formato, parseo y detección de negativa de policy. El motor NO conoce strings backend-específicos (I2).

## Decisión

Agregar un provider `subprocess` con arquitectura **motor genérico + interfaz `Backend`**:

- **Motor genérico** (`subprocess.Provider`, implementa `provider.Provider` intacto — I1): exec del binario, directorio por cliente, sesión estable, serialización por sesión, captura stdout/exit, fabricación de usage, normalización de errores. El motor NO interpreta strings backend-específicos (I2).
- **Interfaz `Backend`** (frontera de traducción): `Name`, `Args(s, model, flags)`, `TranslateReq(body)`, `TranslateOut(raw, model)`, `TranslateStreamOut(lines, ch, model)`, `IsRefusal(stderr)`. El adapter `claude` la implementa en 013-002.
- **Sesión por cliente** (default, D2): `Session.ID` derivado determinísticamente de `auth.ClientIDFrom(ctx)`; override opcional `client+model` implementado en 001 y expuesto por 003. El modelo se pasa por request (`Backend.Args` → `--model`), NO participa de la key de sesión en modo default (P3).
- **Historial en la sesión, prompt único** (D3): mofgw envía SOLO el último mensaje `user` (extraído por `TranslateReq`) como un único prompt limpio vía stdin; no re-construye el historial. El CLI acumula la conversación en su sesión estable.
- **Traducción limpia, sin tools** (D4): el prompt es texto natural; ningún arreglo `tools` llega al CLI desde mofgw (el CLI usa sus propias tools internas como agente).
- **Sin fallback a nivel backend** (D5): un único backend subprocess; una negativa/falla del CLI NO se auto-rutea a otro backend subprocess. Todo fallo se normaliza a `ErrUpstream`; el fallback cross-provider genérico de mofgw aplica si otro provider sirve el mismo modelo.
- **Usage fabricado por el motor** (D7) en Complete y Stream (chunk final `usage` compatible con `CaptureUsage`).
- **Serialización por sesión** (D8): a lo sumo un proceso CLI en vuelo por `Session.ID`; lock por sesión sostenido durante todo el request incluido el stream; espera ctx-cancelable.
- **Errores vía `provider.NewErrUpstream`** (D9): spawn fallido → `network`; exit no-cero → `upstream_error` saneado; timeout/deadline → `timeout`; **refusal de policy → `invalid_request_error`/400 NO retryable** (P11, `IsRefusal` en la frontera Backend); `ctx.Err()`/`DeadlineExceeded` NUNCA se filtran crudos; sanitize en todos los errores (P12); clientID vacío → fail-closed (P13).
- **Tests herméticos** (D6): stub CLI + test-double Backend, sin binario `claude` real ni suscripción, cero red externa.

## Razones

1. **La suscripción de consumidor no tiene API key** → el CLI oficial es la única vía estable de usar Claude Pro; la vía "bendecida" (Anthropic no bloquea su propio binario). Sin reverse-engineering de OAuth.
2. **Backend-agnóstico** (I2): la interfaz `Backend` deja lista la suma de gemini/codex a futuro sin tocar el motor — el motor solo conoce exec/sesión/serialización/errores.
3. **Aprovecha la infraestructura existente**: el provider implementa `provider.Provider` intacto → el router rutea/fallbackea/cooldown/clasifica igual que un provider HTTP (fallback transparente).
4. **Normalización a `ErrUpstream`** (D5/D9): el fallback cross-provider genérico de mofgw aplica automáticamente; el cliente nunca ve errores crudos del CLI. La no-retryabilidad del refusal evita quemar cuota de suscripción rotando providers sin chance de éxito.
5. **Riesgo acotado y verificable**: hermeticidad por diseño (stub CLI + test-double), verificada por la suite completa 531/20 `-race` y la review (0 bloqueantes, relevance audit 19/19).

## Consecuencias

- **Positivas**: reuso de la suscripción (no paga por token), backend-agnosticismo para futuros gemini/codex, fallback transparente vía el router existente, aislamiento por cliente (sesiones separadas por `clientID`).
- **Negativas**: spawn overhead ~1-2s por llamada (mitigado con `--bare` + sesión estable; la latencia del LLM domina); sin fallback entre backends subprocess (D5); cuota de suscripción de ventana rodante se quema rápido con agentes (el CLI reporta rate-limit → mofgw lo absorbe y fallbackea); sin passthrough de `tool_calls` OpenAI ↔ CLI (deuda del epic, el CLI usa sus propias tools).
- **Seguridad**: claude es un agente con acceso a herramientas → `backend_flags` restrictivo por default (`--permission-mode`), solo agentes del usuario (013-003), validar que `ANTHROPIC_API_KEY` no sombree OAuth (riesgo documentado del plan).
- **Deuda transferida a 013-004** (de la review): preservar usage real del backend si no-cero, manejar `sc.Err()` del scanner, allowlist de env, limpieza TTL de sesiones en disco, limpieza del lock map.
- **Frontera estable**: `Backend` queda congelado como contrato cross-feature del epic; el adapter `claude` (013-002) lo implementa sin tocar el motor.
