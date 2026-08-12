---
epic_id: 013-mofgw-cli-subprocess
epic_name: Providers vía subprocess de CLIs (claude hoy; gemini/codex futuro)
created_at: 2026-08-12
approved_by: Ofap (agent-delegated HITL, pedido explícito de Pablo)
approved_at: 2026-08-12
---

# Epic 013: Providers tipo subprocess (claude)

## Resumen

Agregar a mofgw un provider tipo **`subprocess`** que ejecuta el CLI de un backend de IA como subproceso, en vez de hablar HTTP con un endpoint. Esto permite usar la **suscripción Claude Pro** como provider de fallback, sin reverse-engineering de OAuth ni bridges HTTP. Es la vía "bendecida": se usa el CLI tal como Anthropic lo concibe (no van a bloquear su propio binario).

Arquitectura: un **motor subprocess genérico** (sesión por cliente, serialización, exec, captura de salida, errores) + **adapters por backend** vía interfaz `Backend` que solo definen argv, traducción de formato y parseo. En este epic se implementa **solo el adapter `claude`** (único backend testable hoy); la interfaz queda lista para sumar `gemini` y `codex` después como adapters. Se habilita **solo para los agentes del usuario**, no para clientes generales. El modelo se marca **sin tools** (claude usa sus propias internas; mofgw lo ve como modelo de texto).

**Fuera de este epic (decisión usuario 12 Ago):** `gemini` ya no soporta OAuth de suscripción (solo API key) → no aplica al patrón subprocess-suscripción; `codex` no está disponible en el entorno para testear ahora → se agrega después. `qwen` ya no tiene OAuth de suscripción (discontinuado 15 Abr 2026); su auth es API-key OpenAI-compatible, ya soportado por mofgw como provider nativo.

## Scope

**In scope:**
- Motor `subprocess` genérico: exec del binario del backend, directorio por cliente, session-id estable, serialización por sesión, captura stdout/exit, errores → `ErrUpstream`.
- Interfaz `Backend` (argv, traducción, parseo) con `claude` como única implementación en este epic.
- Adapter `claude`: binario `claude`, flag `-p --session-id` (Complete + Stream).
- Traducción OpenAI chat-completions ↔ formato del CLI de claude.
- Sesión modo **prompt-cache**: cada request pasa el prompt completo con session-id estable por cliente; el CLI cachea el prefijo.
- Serialización por sesión (un turno a la vez) + integración con limiter global y singleflight existentes.
- Catálogo `/v1/models`: modelos subprocess marcados **sin tools** (`supported_parameters` sin `tools`); request path omite/rechaza `tools` para estos providers.
- Config: `type: subprocess` + `backend` + `command` + `session_dir` + `models` + `max_tokens` + flags opcionales (`--bare`, `--permission-mode`, etc).
- Manejo de errores del CLI (timeout, rate-limit de suscripción, auth, exit≠0) absorbidos por el fallback transparente.
- Limpieza de sesiones viejas en disco (TTL).
- Restricción de uso: solo agentes del usuario (validado/avisado en config).

**Out of scope:**
- Passthrough de `tool_calls` OpenAI ↔ CLI (el loop de tools lo maneja el backend internamente).
- Adapters `gemini` y `codex` (futuros: gemini sin OAuth de suscripción, codex sin entorno de test hoy). La interfaz `Backend` los acomoda.
- Qwen como subprocess (es provider OpenAI-compatible; ya soportado).
- Soporte para clientes generales.
- Estructured output / JSON-schema nativo vía CLI.
- Bridge HTTP hacia `api.anthropic.com` (alternativa descartada).
- Multi-cuenta / round-robin de suscripciones.

## Decomposición en features

| # | Feature ID | Descripción (1 línea) | Dependencias | Paralelizable |
|---|-----------|------------------------|--------------|---------------|
| 1 | 013-001-subprocess-core | Motor genérico: exec del binario, directorio/sesión por cliente, serialización, captura stdout/exit, errores → ErrUpstream, interfaz Backend | — | Sí |
| 2 | 013-002-adapter-claude | Adapter Claude: argv (`claude -p --session-id`), traducción OpenAI→prompt, parseo salida→chat-completions (Complete+Stream) | 001 | No |
| 3 | 013-003-config-wiring | Config `type: subprocess` + `backend` + factory en router + catálogo sin tools + request path omite/rechaza tools | 001 | Sí |
| 4 | 013-004-resilience-ops | Errores CLI (timeout/rate-limit/auth/exit≠0) normalizados, health check, limpieza de sesiones en disco (TTL) | 001, 002 | No |

## Contratos cross-feature

`Provider` (existente, compartido): `ID() / Complete(ctx, body) / Stream(ctx, body) / MaxTokens() / Serves(model)` — implementado por 013-001 para subprocess.

Contrato nuevo `Backend` (definido por 001, implementado por 002; gemini/codex lo implementarán a futuro):

```go
// Backend define cómo un CLI concreto se invoca y traduce.
// El motor subprocess (013-001) lo llama; cada backend aporta lo específico.
type Backend interface {
    Name() string
    // Args arma el argv del CLI para un request (cwd = Session.Dir).
    Args(s *Session, model string, flags []string) []string
    // TranslateReq traduce el body chat-completions a lo que el backend
    // consume (prompt completo, vía stdin o arg).
    TranslateReq(body []byte) (string, error)
    // TranslateOut traduce stdout del backend a un ChatResponse OpenAI.
    TranslateOut(raw []byte, model string) (*provider.ChatResponse, error)
    // TranslateStreamOut traduce salida stream del backend a eventos SSE.
    TranslateStreamOut(lines <-chan string, ch chan<- provider.StreamEvent, model string)
}
```

```yaml
# Config (usado por 003 para construir el provider):
providers:
  - id: claude-pro
    type: subprocess        # discriminador (nuevo)
    backend: claude         # hoy solo claude; gemini/codex a futuro
    command: claude         # binario del CLI
    session_dir: ~/.config/mofgw/sessions   # base de dirs por cliente
    models: ["claude-sonnet-4-6"]
    max_tokens: 8192
    backend_flags: ["--bare", "--permission-mode", "strict"]  # opcional
```

Usado por: 001, 002, 003.

## Criterios de aceptación del epic

- [ ] Las 4 features están done individualmente (cada una con su gate de Etapa 5 cerrado).
- [ ] E2E principal (claude): con un provider subprocess claude configurado, un request ruteado a él devuelve una completion de texto coherente.
- [ ] E2E sesión: un segundo request del MISMO cliente usa el mismo dir + session-id y conserva contexto (prompt-cache, sin duplicar).
- [ ] E2E aislamiento: dos clientes distintos tienen dirs separados y no comparten sesión/contexto.
- [ ] E2E sin tools: `/v1/models` marca el modelo claude sin `supported_parameters.tools`; un request con `tools` NO se envía al CLI (se rechaza o se omite).
- [ ] E2E fallback: un error del CLI (timeout/rate-limit/exit≠0) se absorbe y mofgw rota al siguiente provider (el cliente nunca lo ve).
- [ ] Serialización: dos requests concurrentes del mismo cliente se serializan (nunca dos spawns simultáneos por sesión).
- [ ] Suite completa verde con `-race`, `go vet` y `gofmt` limpios.
- [ ] Smoke test real (verificación manual, fuera de la suite): un request contra el CLI `claude` autenticado devuelve una completion coherente. Los tests automatizados usan un **stub del CLI** (hermeticidad: la suite no depende del CLI real ni de la suscripción).

## Riesgos / deuda esperada

- Riesgo: **cuota de suscripción** (Claude Pro) — ventana rodante se quema rápido con agentes. Mitigación: el CLI reporta rate-limit; mofgw lo absorbe y hace fallback (transparencia). Documentar límites.
- Riesgo: **seguridad** — claude es un agente con acceso a herramientas (Bash/Edit/Read). Mitigación: `backend_flags` restrictivo por default (`--permission-mode`), solo agentes del usuario, documentado.
- Riesgo: **spawn overhead** ~1-2s por llamada (Node). Mitigación: `--bare` + prompt-cache vía sesión; la latencia del LLM domina.
- Riesgo: **`ANTHROPIC_API_KEY` sombrea OAuth** en el entorno → cobra pay-per-use sin avisar. Mitigación: validar auth al arrancar; documentar.
- Riesgo: **sesiones acumuladas en disco**. Mitigación: TTL/limpieza (013-004).
- Deuda esperada: passthrough de tools y structured output quedan fuera (no aplican al CLI headless en esta versión). Adapters gemini/codex pendientes de sumar cuando estén disponibles (sin cambios en el motor).

## Stakeholders

- **Aprobador del plan del epic**: Pablo
- **Aprobador de specs de features**: Pablo
- **Operador del resultado**: agentes propios del usuario (solo ellos habilitados)

## Cambios al plan

### 2026-08-12 — Generalización claude→subprocess
Motivo: el usuario pidió soportar también gemini y qwen con el mismo método.
Cambio: `type: claude` → `type: subprocess` + `backend` con interfaz `Backend` y motor genérico. **Qwen queda fuera** tras verificar que su auth es API-key OpenAI-compatible (discontinuado OAuth 15 Abr 2026) — ya soportado como provider nativo.

### 2026-08-12 — Quitar gemini y codex del epic (quedan como adapters futuros)
Motivo: gemini ya no soporta OAuth de suscripción (solo API key) → no aplica al patrón subprocess; codex no está disponible en el entorno para testear ahora.
Cambio: se eliminan 013-004-adapter-gemini y 013-005-adapter-codex de la decomposición. El epic queda con 4 features (core, claude, config, resilience-ops). La interfaz `Backend` queda lista para sumarlos a futuro sin tocar el motor.

---

Status: Approved by Ofap (agent-delegated HITL, pedido explícito de Pablo) on 2026-08-12
