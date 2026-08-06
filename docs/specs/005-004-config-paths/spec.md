# Spec — 005-004-config-paths: Resolución de config por nivel (home vs sistema)

---
feature_id: 005-004-config-paths
epic: mofgw-005-ops
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-002-config
paralelizable: sí
---

```yaml
context: >
  001-002 ya define la carga y validación del YAML, incluyendo la
  precedencia home/sistema (~/.config/mofgw/config.yaml gana sobre
  /etc/mofgw/config.yaml). Esta feature lo hace OPERABLE: un usuario
  de hosting (agente cliente, 001-005-adjacent) no debe necesitar editar
  /etc — su config vive en su home; un deploy de sistema (root,
  systemd) usa /etc. El problema que resuelve: sin un mecanismo
  claro, cada deploy inventa su propio path, los flags se pelean con
  los archivos, y "¿dónde está mi config?" es una pregunta recurrente
  en soporte. El alcance: definir la resolución COMPLETA de paths —
  config, log level override, y la precedencia de flag > env >
  home > sistema — y documentarla en un solo lugar canónico
  (README + `mofgw --help` + config-path subcommand).
resolution: >
  Resolución de config en orden de precedencia (mayor gana):
  1. Flag `--config /ruta/explicita` (ya definido en 001-002: error
     claro si la ruta no existe);
  2. Env var `MOFGW_CONFIG` (ruta explícita, mismo comportamiento
     que el flag);
  3. `$XDG_CONFIG_HOME/mofgw/config.yaml` si XDG_CONFIG_HOME está
     set; si no, `$HOME/.config/mofgw/config.yaml`;
  4. `/etc/mofgw/config.yaml` (nivel sistema);
  5. Ninguna existe → error fail-fast de 001-002 con las rutas
     intentadas, exit 1.
  Notas de la resolución:
  - Si flag O env están set, se usa SOLO esa ruta (no se intenta
    home/sistema). Si la ruta no existe → error claro (nunca
    silent-fallback a home).
  - Si no hay flag/env, se evalúa home primero y sistema después;
    el primero que existe gana (consistente con 001-002: ambos →
    gana home).
  - La ruta RESUELTA se loguea en el evento startup (005-002) y se
    expone vía `mofgw config-path` (subcomando que imprime la ruta
    que se usaría, sin cargar: útil para debugging y scripts).
  - El directorio de config NO se crea automáticamente (solo el
    install script de 005-003 copia un config.yaml de ejemplo si no
    existe).
  - Permisos: config en home con 0600 recomendado (contiene
    referencias a env vars, no keys, pero igual); se loguea un warn
    si el archivo es world-readable (>0644) — defensa en profundidad.
postcondition: >
  mofgw arranca con la config correcta sin importar cómo se deployó:
  home para usuarios, /etc para sistema, flag/env para overrides
  explícitos. La ruta usada es visible (startup log + config-path) y
  la precedencia está documentada en README y --help.
verifications: >
  - `go test ./internal/config/` → suite completa de 001-002 +
    casos nuevos: MOFGW_CONFIG set → usa esa ruta; XDG_CONFIG_HOME
    set (sin flag/env) → usa $XDG_CONFIG_HOME/mofgw/config.yaml;
    ni flag ni env → home gana sobre /etc; flag + env + home + /etc
    todos set → gana flag.
  - `mofgw config-path` imprime la ruta correcta en cada escenario
    (mismos casos, sin cargar el YAML).
  - Ruta explícita inexistente (flag o env) → error claro con la ruta,
    exit 1, sin fallback silencioso.
  - Config world-readable → warn en startup log (005-002).
  - README actualizado con la tabla de precedencia completa.
```

## Contratos con otras features

- **001-002-config:** la carga/validación del YAML y el flag `--config` quedan como están; 005-004 añade el env `MOFGW_CONFIG` y el subcomando `config-path` sin cambiar la semántica de validación.
- **005-002-logging:** el evento `startup` incluye `config_path` (ruta resuelta) y emite el warn de permisos si aplica.
- **005-003-systemd:** el unit systemd usa la resolución estándar (root → /etc); los agentes clientes  usan home automáticamente por ser usuarios sin privilegios — el mismo binario, cero flags.
- **001-005-streaming / 001-003-fallback:** no les afecta; es carga de config, no ruteo.

## Riesgos y mitigaciones

- **Confusión flag vs env:** documentación explícita en README (tabla de precedencia) + `--help` muestra ambos; el orden flag > env está testeado.
- **XDG_CONFIG_HOME vacío vs unset:** se trata igual que unset (fallback a $HOME/.config) — testeado.
- **Home inexistente (systemd root edge):** si $HOME no está set (caso raro en daemons), se salta a /etc sin error — testeado con HOME unset.
