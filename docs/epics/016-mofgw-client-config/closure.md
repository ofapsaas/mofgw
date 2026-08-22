# Closure — Epic 016-mofgw-client-config

> E4 · Cierre 2026-08-22 · Epic COMPLETO: 4/4 features MERGED + integración cross-feature E2E verificada + Memory Bank actualizado.

## Resumen

El epic **mofgw-client-config** agrega un endpoint que devuelve el **fragmento de configuración del provider mofgw listo para insertar** en un cliente soportado, generado desde el catálogo real (no hardcodeado), para sincronizar la config de cada cliente cuando los providers upstream actualizan modelos/capacidades.

**Entregado:** `GET /v1/client-config?client=<id>` (autenticado bajo /v1) + IR tipado + registry de renderers + **3 adapters** (opencode, openclaw, zot) registrados en producción. Cada adapter serializa el IR al formato exacto de su cliente (verificado por research, no inventado).

**Suite:** **697 tests `-race` en 27 paquetes** verde. Cero regresión.

## Features del epic (4/4)

| # | Feature | Qué entrega | Commits |
|---|---|---|---|
| 016-001 | config-renderer-core | endpoint `/v1/client-config` + IR (ConfigIR/ModelEntry) + registry público + knob `client_config{base_url,key_env}`; orden 400-antes-503; errores openAIError | spec a7928e0, RED a04cdf8, GREEN 3f28fd3, review 75a7814 |
| 016-002 | adapter-opencode | fragmento JSON para `opencode.json` (provider "mofgw": `{env:VAR}` template, `npm: @ai-sdk/openai-compatible`) | spec af4d756, RED 842f183, GREEN d5dcaa1, fix 15bd538 |
| 016-003 | adapter-openclaw | fragmento JSON5 para `~/.openclaw/openclaw.json` (`${VAR}` template, `models[]` array, `api:"openai-completions"`) | spec 124594a, RED d4eb6cd, GREEN 8eb16e6, review 4dd5459 |
| 016-004 | adapter-zot | fragmento JSON para `$ZOT_HOME/models.json` (sin campo apiKey; env derivada `MOFGW_API_KEY`) | spec 8513903, RED 99e608b, GREEN 8ea10ff, review ba459f4 |
| — | integración | wiring de los 3 adapters en `cmd/mofgw/main.go` + E2E cross-feature | a8af57e, 7424b83 |

## Decisiones clave del epic

- **Los clientes usan el MISMO IR, pero cada uno tiene su propio shape/sintaxis de env-ref.** Investigado, no asumido: opencode = `{env:VAR}` (template string, no objeto) + `npm`; openclaw = `${VAR}` + JSON5 `models` array; zot = **sin campo key** (env derivada `MOFGW_API_KEY`). El epic corrió research real por adapter — esto es el valor central (evita fragmentos que el cliente no entiende).
- **El IR es el contrato cross-feature:** `ConfigIR{ClientID, BaseURL, KeyEnvRef, Models []ModelEntry}` reusa `modelCatalogEntry` (misma fuente de verdad que `/v1/models`); los 3 adapters lo consumen. La key viaja **siempre** como env-ref (o ausente en zot), nunca el literal (I1).
- **Aditividad total (I3):** única ruta nueva, `/v1/*` intacto, knob off-safe. Cero llamadas salientes nuevas.
- **Patrón adapter consolidado:** renderer puro de serialización, determinista por `encoding/json` (sort de keys), guard >0 + fallback-rule (ausente≠0) — lección del hallazgo #1 de 016-002 aplicada desde RED en 003 y 004.

## Retrospectiva breve

- **Research > suposición:** las 3 features de adapter requirieron research del shape real de cada cliente; en opencode el research corrigió el shape (`{env:VAR}` string, no objeto) y en openclaw el formato completo (JSON5, `${VAR}`). El costo del research fue bajo y evitó fragmentos rotos.
- **El canal `delegate` del arnés estaba roto** (3 fallos con read-only architect); se resolvió usando `task` con un carrier que ejecuta el contrato del rol. Funcionó bien para el resto del ciclo.
- **Review atrapó 1 hallazgo Importante** en 016-002 (emisión de `limit:0`) que era real y alcanzable desde el catálogo; se aplicó la lección como postcondición P12 en 003 y 004 desde RED (no repetido).

## Deuda técnica llevada

- **Registro de adapters en producción hoy es declarativo en `main.go`** (los 3 renderers). Si se agregan más clientes, el patrón es añadir el paquete + `clientconfig.Register`. No hay "auto-descubrimiento" ni configuración por-config del set de adapters expuestos — aceptable, alineado al scope.
- **Campos opcionales por-cliente no emitidos:** openclaw (`reasoning`/`input`/`cost`) y zot (`reasoning`/`pricing`/`input`) se omiten por minimalismo (defaults del cliente los cubren); enriquecerlos desde `Meta.modality`/`capabilities`/pricing requiere verificar contra el catálogo real → epic de hardening futuro.
- **`models.mode=="merge"`** de openclaw sin postcondición propia (default del cliente; impl lo emite) — no aplicado (churn marginal).
- **E2E real manual pendiente:** probar el fragmento openclaw/zot contra un cliente real (no mock). Opcional por no haber fixture-drift.

## Cierre

- [x] `docs/epics/016-mofgw-client-config/closure.md` (este).
- [x] `docs/activeContext.md` entry de cierre del epic.
- [x] State: epic 016 → done, `epic_stage` cerrado.
- [x] 4/4 features done + integración E2E verde (697 tests).
- [x] Sin ADRs nuevos (feature aditivas sin frontera arquitectónica nueva; el "contrato IR" quedó en el plan del epic y specs).

---