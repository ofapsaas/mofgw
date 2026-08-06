# Spec — 005-003-systemd: Unit systemd user + install script

---
feature_id: 005-003-systemd
epic: mofgw-005-ops
status: draft (pendiente aprobación plan epics)
created_at: 2026-08-04
updated_at: 2026-08-04
depends_on: 001-001-endpoint
paralelizable: sí
---

```yaml
context: >
  mofgw es un proxy de IA self-hosted pensado para correr como servicio
  de USUARIO (decisión Pablo 03 Ago: "deploy systemd user básico").
  Sin una unit systemd y un install script, cada deploy inventa su
  propia forma de correr el proceso: nohup, tmux, crontab — nada
  sobrevive un reboot ni se auto-reinicia. El patrón de referencia
  ya existe en el ecosistema: unidades systemd user de agentes
  (template validado con systemd-analyze verify) y units ya
  instaladas y verificadas. Esta feature hace que mofgw se instale y
  administre con
  `systemctl --user` igual que el resto de los servicios de usuario: un solo
  comando de instalación, auto-start, restart on failure, logs por
  journald (consumidos por 005-002), y config inicial provista por
  el propio script (sin pasos manuales).
resolution: >
  Un install script (`scripts/install.sh`) idempotente que:
  1. Copia el binario mofgw a `~/.local/bin/mofgw` (755);
  2. Instala `mofgw.service` (unit user) en
     `~/.config/systemd/user/mofgw.service`;
  3. Copia `config.example.yaml` → config real si no existe, usando
     la resolución de 005-004 (home por defecto:
     `~/.config/mofgw/config.yaml`; si se instala como root y
     MOFGW_CONFIG apunta a /etc, copia ahí);
  4. Corre `systemctl --user daemon-reload` + `enable --now`;
  5. Verifica con `systemctl --user is-active mofgw` y un curl al
     endpoint de health de 002-002 (o, pre-002-002, un TCP connect
     al puerto 3369).
  La unit (contrato mínimo):
  - `[Unit]` Description, `After=network-online.target`,
    `Wants=network-online.target`;
  - `[Service]` `ExecStart=%h/.local/bin/mofgw` (o ruta del
    binario instalado), `Restart=on-failure`,
    `RestartSec=5`, `Environment=MOFGW_CONFIG=...` opcional
    (solo si el deploy necesita override explícito),
    `StandardOutput=journal`, `StandardError=journal`,
    `TimeoutStartSec=30`;
  - `[Install]` `WantedBy=default.target`.
  Notas de resolución:
  - El unit corre SIEMPRE como el usuario que lo instaló (sin
    sudo, sin root). Para que sobreviva sin sesión abierta el
    usuario necesita linger: `loginctl enable-linger <user>`
    (requisito estándar de servicios user-level).
  - En un servidor de hosting hay UN mofgw por servidor
    (decisión 04 Ago): se instala con el usuario técnico
    del servidor; los agentes clientes de cada cuenta apuntan a
    `http://<mofgw-host>:3369/v1` sin keys propias.
  - El script NO edita configs existentes (nunca sobrescribe un
    config.yaml con keys reales); solo crea el ejemplo si no hay
    nada. Eliminar/reinstalar config es decisión manual.
  - Si el puerto 3369 ya está ocupado al instalar → el script
    falla con mensaje claro (no intenta matar el proceso ajeno).
  - Desinstalación: `scripts/install.sh --uninstall` → stop +
    disable + borra unit y binario, conserva config (mueve a
    `config.yaml.bak.<ts>` si existe).
  - Restricción de versión: los campos de unit usados
    (`RestartSec`, `TimeoutStartSec`) son POSIX systemd (v219+);
    no se usan features de systemd reciente (>=240) salvo que el
    código lo detecte y degrade.
postcondition: >
  `./scripts/install.sh` en una máquina limpia deja mofgw corriendo
  como servicio de usuario, habilitado para auto-start, logueando a
  journald en JSON (005-002), con config de ejemplo creada y visible
  vía `systemctl --user status`. El mismo script funciona como root
  (deploy de sistema con /etc/mofgw/) y como usuario de hosting
  (deploy home). Reboot → mofgw vuelve solo (con linger).
verifications: >
  - `./scripts/install.sh` (usuario normal) → exit 0;
    `systemctl --user is-enabled mofgw` → enabled;
    `systemctl --user is-active mofgw` → active.
  - `systemctl --user status mofgw` muestra ExecStart apuntando a
    `~/.local/bin/mofgw` y log de startup en journald (formato
    JSON de 005-002).
  - `test -f ~/.config/mofgw/config.yaml` → existe y es el ejemplo
    (o el provisto por MOFGW_CONFIG); `mofgw config-path` (005-004)
    imprime la ruta usada.
  - `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:3369/v1/models`
    → 200 con el binario corriendo (001-001).
  - Reinicio: `systemctl --user restart mofgw` → sigue activo,
    nuevo log de startup, sin error de "address already in use".
  - `systemctl --user stop mofgw` + `start` → arranca limpio;
    `kill -9` del proceso → Restart=on-failure lo levanta en ≤10s.
  - Re-instalación (segunda corrida del script) → idempotente:
    exit 0, sin duplicar units, sin tocar config existente.
  - `scripts/install.sh --uninstall` → unit y binario removidos,
    config preservada como backup; `systemctl --user status` →
    "Unit mofgw.service could not be found".
  - `systemd-analyze verify ~/.config/systemd/user/mofgw.service`
    → sin errores (solo warnings ajenos al unit, mismo estándar
    del ecosistema).
  - Con linger habilitado y sin sesión (ssh + logout): proceso
    sigue vivo y responde en 3369.
```

## Notas de implementación (para CDAD implementer)

- Estructura: `scripts/install.sh` (bash, stdlib, sin deps) +
  `systemd/mofgw.service` (unit template) + `config.example.yaml`
  (fuente canónica del ejemplo; 001-002 ya define el schema).
- El script debe ser seguro para re-correr (idempotencia por
  checksum de la unit: no sobrescribe si el contenido es idéntico).
- Detección de modo: `EUID==0` → ruta sistema (`/etc/mofgw/`,
  unit en `/etc/systemd/user/` no existe → usar
  `/root/.config/systemd/user/` o documentar deploy root como
  "same as user, home=/root"); `EUID!=0` → home. La regla de oro:
  la unit vive en `$HOME/.config/systemd/user/` del usuario que
  ejecuta, sea root o no (consistente con 005-004: la config
  puede apuntar a /etc vía MOFGW_CONFIG, pero el servicio siempre
  es user-level).
- La config de ejemplo NUNCA contiene keys reales — solo
  placeholders (`<provider-api-key-env>`) y referencias a env
  vars, consistente con la política de skills (nunca datos reales).
- Relación con el ecosistema: cuando mofgw esté en línea en un
  servidor, el paso final del deploy (activar la unit del agente
  cliente) usa esta install para levantar mofgw primero.

## Criterio de aceptación (checklist CDAD)

- [ ] install.sh idempotente (2 corridas → mismo estado)
- [ ] unit valida con systemd-analyze verify
- [ ] auto-restart tras kill -9 (≤10s, RestartSec=5)
- [ ] logs JSON en journald (integra 005-002)
- [ ] config de ejemplo creada sin sobrescribir existentes
- [ ] --uninstall remueve y preserva config como backup
- [ ] funciona como usuario normal y como root (home=/root)
