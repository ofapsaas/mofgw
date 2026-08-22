# EPIC-016 — mofgw-client-config

> "Un endpoint que devuelve el fragmento de config del provider mofgw listo para insertar en cada cliente soportado."

## Resumen

Los providers upstream actualizan modelos y capacidades con frecuencia, y la config del cliente (opencode.json, openclaw.conf, zot, …) se sincroniza a mano. Este epic agrega un endpoint en mofgw que, dado un cliente, devuelve el **fragmento de configuración del provider mofgw** en el formato exacto de ese cliente, listo para insertar — generado desde el catálogo real (007-002/010-001), no hardcodeado.

**Alcance de ESTE epic:** endpoint read-only + renderers por cliente. Nada más dentro de mofgw.

**Fuera de alcance:** la escritura automática en los archivos de config de los clientes (mofgw no toca el filesystem del host), ni el "client bootstrap" end-to-end. El output es copy-paste-ready; quien lo aplique lo aplica.

## Decisiones de discovery (confirmadas 2026-08-22)

1. **Catálogo completo**: el fragmento incluye por modelo la metadata completa que ya emite `modelCatalogEntry` (context_length, max_output_tokens, supported_parameters, capabilities, modality/architecture, pricing?, thinking). No formato mínimo.
2. **`base_url` configurable**: no hardcodeada. Sale de config nueva (knob `client_config.base_url`), con default sensato.
3. **Key por referencia**: el fragmento **nunca** contiene el secret; emite referencia a env var (p.ej. `{env:MOFGW_KEY}` / patrón del cliente). Endpoint sin credenciales sensibles en la respuesta.
4. **Sin mitigación por test-fixture por template** (descartada por Pablo). Los renderers se prueban con lo que el cliente mismo define; el contrato del renderer es el type, no un fixture external.

## Features

| # | Feature | Qué entrega |
|---|---------|-------------|
| `016-001` | **config-renderer-core** | Endpoint `GET /v1/client-config?client=<id>` + IR (representación intermedia: base_url, key env-ref, models con metadata del catálogo) + registro de renderers + knob `client_config.base_url`. |
| `016-002` | **adapter-opencode** | Renderer que emite el fragmento JSON para `opencode.json` (responsabilidad de provider HTTP compatible con la estructura que opencode ya entiende). |
| `016-003` | **adapter-openclaw** | Renderer que emite el fragmento para `openclaw.conf`. |
| `016-004` | **adapter-zot** | Renderer que emite el fragmento para zot. |

**016-001 — detalle (el core):**

- Nuevo paquete `internal/clientconfig` (patrón renderer + registry, sin dependencias salientes nuevas — cero llamadas HTTP fuera, ADR-005 no aplica).
- IR tipado: `{ClientID, BaseURL, KeyEnvRef, Models: []CatalogEntry}` donde `CatalogEntry` reusa el enriquecimiento de `modelCatalogEntry` (misma fuente de verdad: catálogo en memoria de 007-002/010-001).
- Registro de renderers: `map[string]Renderer` con interfaz `Renderer.Render(ir) ([]byte, error)`. Cliente desconocido → 404 con lista de clientes soportados.
- Endpoint detrás de la misma auth del resto de /v1 (clientID autenticado, 401 si no).
- Knob `client_config:` en config (aditivo, off-safe): `{base_url: "", key_env: "MOFGW_KEY"}`. `base_url` vacío → 503 "client_config.base_url not set".
- Key viaja **solo como env-ref**: el IR tiene `KeyEnvRef`, nunca el valor.

**016-002/003/004 — detalle (adapters):**

- Cada uno es un `Renderer` en `internal/clientconfig/opencode|openclaw|zot` (o subpaquetes).
- Contrato único: mismo IR → fragmento en el formato del cliente. El shape exacto se resuelve en la feature (research de la estructura real del config de cada cliente + tests contra ese shape).
- Orden de entrega: 002 opencode (más usado), 003 openclaw, 004 zot (paralelizables entre sí, todas dependen de 001).

## Contrato cross-feature

El **IR** es el contrato compartido por todos los renderers (001 lo define, 002/003/004 lo consumen).

```go
// internal/clientconfig
type ConfigIR struct {
    ClientID   string
    BaseURL    string   // knob client_config.base_url (alive en el IR)
    KeyEnvRef  string   // p.ej. "MOFGW_KEY" — NUNCA el valor
    Models     []ModelEntry
}
type ModelEntry struct {
    ID       string
    // metadata del catálogo de 007-002/010-001 (context_length,
    // max_output_tokens, supported_parameters, capabilities, modality…)
    Meta     map[string]any
}
type Renderer interface {
    Render(ir ConfigIR) ([]byte, error)
}
```

Usado por: 016-001 (define), 016-002/003/004 (consumen).

## Criterios de aceptación del epic

1. Las 4 features done individualmente.
2. E2E: `GET /v1/client-config?client=opencode` devuelve el fragmento JSON **fiel al catálogo actual** (los modelos listados == los de `/v1/models`; la metadata por modelo coincide con la del catálogo, no hardcodeada).
3. E2E: con `client_config.base_url` seteado, el fragmento usa ese base_url en todos los renderers.
4. E2E: en ningún renderer aparece el valor de la API key — solo la env-ref.
5. E2E de fallo: `client=desconocido` → 404 con la lista de clientes soportados; `base_url` vacío → 503.
6. E2E: `client=openclaw` y `client=zot` devuelven un fragmento en su formato (parseable según el cliente), fiel al mismo IR.
7. Suite completa `-race` verde; endpoint aditivo, cero regresión en /v1/* existente (incluido el catálogo).

## Qué NO entra (anti-scope)

- ❌ No escribir/parsear archivos de config del host (no hay filesystem I/O del lado del servidor).
- ❌ No validar sintáctica del fragmento contra el schema del cliente (sin fixture-drift mitigation).
- ❌ No cubrir el bootstrap del cliente (instalar key, montar túnel).
- ❌ No cambiar `/v1/models` ni el enriquecimiento del catálogo (se consume tal cual).

## Riesgos / deuda esperada

- Riesgo: el shape exacto del config de cada cliente evoluciona (opencode/openclaw/zot cambian su schema). Mitigación: los renderers son delgados y aislados en subpaquetes; el contrato IR no cambia con el schema del cliente. Docs reales de cada formato se verifican en la feature (context7 / docs del cliente).
- Deuda esperada: el endpoint emite el fragmento pero no hay "diff-check" automático contra el config ya insertado del host (fuera de alcance).

## Ejecución

Se materializa como slices del worker `mofgw` (project-tracker, modo `iter`/`cdad`). El worker consume `projects/mofgw/task_plan.md`; antes de lanzar, agregar las features 016-001..004 al task_plan (quitando el marcador "auto-queue: exhausted" si aplica, y `worker-manager.sh sync`).

## Stakeholders

- **Aprobador del plan del epic**: Pablo
- **Aprobador de specs de features**: Pablo (HITL en cada spec)
- **Operador del resultado**: Pablo (consume el endpoint desde cada cliente)

## Cambios al plan

_Inicialmente vacía._

---

Status: **Approved by Pablo on 2026-08-22** (mismo chat, green-light "orquesta el desarrollo")