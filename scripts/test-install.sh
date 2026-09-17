#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later
# test-install.sh — Harness de contrato para 005-003-systemd (scripts/install.sh).
#
# Corre install.sh en un SANDBOX: MOFGW_HOME=$(mktemp -d), MOFGW_SKIP_SYSTEMCTL=1.
# Nunca toca systemd real ni ~/.config real. El binario lo construye/copia
# el propio install.sh (ver su documentación sobre MOFGW_BIN_SRC).
#
# Uso: bash scripts/test-install.sh
# Salida: PASS/FAIL por test; exit 0 solo si TODOS pasan.
#
# Fase RED: si scripts/install.sh aún no existe, el harness falla claro
# (resultado esperado hasta implementar el script).
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_SCRIPT="$REPO_ROOT/scripts/install.sh"
EXAMPLE_CONFIG="$REPO_ROOT/config.example.yaml"

PASS=0
FAIL=0
SANDBOXES=()

say()  { printf '%s\n' "$*"; }
pass() { PASS=$((PASS + 1)); printf '  PASS: %s\n' "$*"; }
fail() { FAIL=$((FAIL + 1)); printf '  FAIL: %s\n' "$*"; }

# Guard RED: sin install.sh no hay nada que testear.
if [[ ! -x "$INSTALL_SCRIPT" ]]; then
  say ""
  say "RED: $INSTALL_SCRIPT no existe o no es ejecutable."
  say "Este es el resultado ESPERADO en la fase RED (solo existe el harness)."
  say "Una vez implementado scripts/install.sh, este harness debe pasar 100%."
  exit 1
fi

# run_install: ejecuta install.sh dentro del sandbox (dry-run de systemd).
run_install() {
  ( cd "$REPO_ROOT" \
      && MOFGW_HOME="$SANDBOX" MOFGW_SKIP_SYSTEMCTL=1 \
           "$INSTALL_SCRIPT" "$@" >"$SANDBOX/install.log" 2>&1 )
}

new_sandbox() {
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/mofgw-install-test.XXXXXX")"
  SANDBOXES+=("$SANDBOX")
}

cleanup() {
  local d
  for d in "${SANDBOXES[@]:-}"; do
    rm -rf "$d"
  done
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Test 1: install crea binario, unit y config (copia del ejemplo).
# ---------------------------------------------------------------------------
test_install_creates_files() {
  new_sandbox
  local bin="$SANDBOX/.local/bin/mofgw"
  local unit="$SANDBOX/.config/systemd/user/mofgw.service"
  local conf="$SANDBOX/.config/mofgw/config.yaml"

  if run_install; then
    pass "install.sh exit 0"
  else
    fail "install.sh exit != 0"; tail -8 "$SANDBOX/install.log" >&2; return
  fi

  [[ -f "$bin" ]] && pass "binario existe: $bin" || fail "binario no existe: $bin"
  [[ -f "$unit" ]] && pass "unit existe: $unit"  || fail "unit no existe: $unit"
  [[ -f "$conf" ]] && pass "config existe: $conf" || fail "config no existe: $conf"

  if [[ -f "$conf" && -f "$EXAMPLE_CONFIG" ]] && cmp -s "$conf" "$EXAMPLE_CONFIG"; then
    pass "config es copia exacta de config.example.yaml"
  else
    fail "config NO es copia exacta del ejemplo"
  fi
}

# ---------------------------------------------------------------------------
# Test 2: la unit pasa systemd-analyze verify (warnings ajenos ok).
# ---------------------------------------------------------------------------
test_unit_passes_verify() {
  new_sandbox
  local unit="$SANDBOX/.config/systemd/user/mofgw.service"

  run_install || { fail "install.sh falló"; return; }

  if systemd-analyze verify "$unit" >"$SANDBOX/verify.log" 2>&1; then
    pass "systemd-analyze verify exit 0"
  else
    fail "systemd-analyze verify exit != 0"
    cat "$SANDBOX/verify.log" >&2
  fi
}

# ---------------------------------------------------------------------------
# Test 3: idempotencia — 2 corridas exit 0, unit única, config intacta.
# ---------------------------------------------------------------------------
test_idempotent() {
  new_sandbox
  local unit="$SANDBOX/.config/systemd/user/mofgw.service"
  local conf="$SANDBOX/.config/mofgw/config.yaml"
  local unit_dir
  unit_dir="$(dirname "$unit")"

  run_install || { fail "primera corrida falló"; return; }
  run_install || { fail "segunda corrida falló"; return; }

  local n before after
  n="$(find "$unit_dir" -maxdepth 1 -name 'mofgw.service' | wc -l | tr -d ' ')"
  if [[ "$n" == "1" ]]; then
    pass "unit única tras 2 corridas (n=$n)"
  else
    fail "unit duplicada tras 2 corridas (n=$n)"
  fi

  before="$(cat "$conf")"
  run_install || { fail "tercera corrida falló"; return; }
  after="$(cat "$conf")"
  if [[ "$before" == "$after" ]]; then
    pass "config no se sobrescribe (mismo contenido tras corrida posterior)"
  else
    fail "config fue sobrescrita en corrida posterior"
  fi
}

# ---------------------------------------------------------------------------
# Test 4: --uninstall borra unit y binario, preserva config como .bak.
# ---------------------------------------------------------------------------
test_uninstall() {
  new_sandbox
  local bin="$SANDBOX/.local/bin/mofgw"
  local unit="$SANDBOX/.config/systemd/user/mofgw.service"
  local conf_dir="$SANDBOX/.config/mofgw"

  run_install || { fail "install.sh falló"; return; }

  if run_install --uninstall; then
    pass "uninstall exit 0"
  else
    fail "uninstall exit != 0"; tail -8 "$SANDBOX/install.log" >&2; return
  fi

  [[ ! -e "$unit" ]] && pass "unit eliminada"  || fail "unit sigue existiendo"
  [[ ! -e "$bin" ]]  && pass "binario eliminado" || fail "binario sigue existiendo"
  if compgen -G "$conf_dir/config.yaml.bak.*" >/dev/null; then
    pass "config preservada como config.yaml.bak.*"
  else
    fail "no hay backup config.yaml.bak.* en $conf_dir"
  fi
}

# ---------------------------------------------------------------------------
# Test 5: config pre-existente NO se pisa.
# ---------------------------------------------------------------------------
test_respects_existing_config() {
  new_sandbox
  local conf_dir="$SANDBOX/.config/mofgw"
  local conf="$conf_dir/config.yaml"
  mkdir -p "$conf_dir"
  printf 'custom\n' >"$conf"

  run_install || { fail "install.sh falló"; return; }

  local content
  content="$(cat "$conf")"
  if [[ "$content" == "custom" ]]; then
    pass "config pre-existente intacta ('custom')"
  else
    fail "config pre-existente fue pisada (contenido: '$content')"
  fi
}

# ---------------------------------------------------------------------------
# Test 6: el binario copiado tiene permisos 755 (o 700+).
# ---------------------------------------------------------------------------
test_bin_permissions() {
  new_sandbox
  local bin="$SANDBOX/.local/bin/mofgw"

  run_install || { fail "install.sh falló"; return; }

  local mode
  mode="$(stat -c '%a' "$bin" 2>/dev/null || printf '000')"
  if [[ "$mode" =~ ^7[0-7][0-7]$ ]]; then
    pass "permisos del binario = $mode (700+)"
  else
    fail "permisos del binario = $mode (esperado 755/700+)"
  fi
}

# run_install_noskip: ejecuta install.sh SIN la guardia dry-run, con un
# `systemctl` FAKE al frente del PATH (sandbox-local, registra llamadas) —
# para testear el gating de orden (P6 de 019-006) sin tocar systemd real.
# El fake: `is-active *` → exit 1 (service no activo); lo demás → exit 0.
run_install_noskip() {
  mkdir -p "$SANDBOX/fakebin"
  cat >"$SANDBOX/fakebin/systemctl" <<'EOF'
#!/usr/bin/env bash
printf 'systemctl %s\n' "$*" >>"$MOFGW_HOME/systemctl.calls"
case " $* " in
  *" is-active "*) exit 1 ;;
esac
exit 0
EOF
  chmod 755 "$SANDBOX/fakebin/systemctl"
  ( cd "$REPO_ROOT" \
      && MOFGW_HOME="$SANDBOX" PATH="$SANDBOX/fakebin:$PATH" \
           "$INSTALL_SCRIPT" "$@" >"$SANDBOX/install.log" 2>&1 )
}

# ---------------------------------------------------------------------------
# Test 2b (019-006 C9): las TRES units pasan systemd-analyze verify.
#
# Nota de entorno (honestidad del harness): systemd-analyze expande %h al
# home REAL del host. El service del sync referencia
# %h/.local/bin/mofgw-sync — el binario solo existe ahí si 006 está
# deployado en el host (territorio del canary B4, no de este harness).
# Este test distingue el error ESPERADO de sandbox ("is not executable" por
# el binario ausente) de errores REALES de sintaxis: lo primero se tolera
# con nota explícita; lo segundo falla. El contenido del unit ya está
# congelado por el golden test Go (C1/C2) — verify solo valida sintaxis.
# ---------------------------------------------------------------------------
test_unit_passes_verify_all() {
  new_sandbox
  local udir="$SANDBOX/.config/systemd/user"

  run_install || { fail "install.sh falló"; return; }

  local u
  for u in mofgw.service mofgw-sync.timer; do
    if [[ ! -f "$udir/$u" ]]; then
      fail "unit ausente para verify: $u"
    elif systemd-analyze verify "$udir/$u" >"$SANDBOX/verify-$u.log" 2>&1; then
      pass "systemd-analyze verify $u exit 0"
    else
      fail "systemd-analyze verify $u exit != 0"
      cat "$SANDBOX/verify-$u.log" >&2
    fi
  done

  # mofgw-sync.service: tolera SOLO el error de binario ausente en sandbox.
  if [[ ! -f "$udir/mofgw-sync.service" ]]; then
    fail "unit ausente para verify: mofgw-sync.service"
  elif systemd-analyze verify "$udir/mofgw-sync.service" >"$SANDBOX/verify-mofgw-sync.service.log" 2>&1; then
    pass "systemd-analyze verify mofgw-sync.service exit 0"
  else
    local log="$SANDBOX/verify-mofgw-sync.service.log"
    # Errores reales = cualquier línea que NO sea el binario ausente del sandbox.
    if grep -vE "is not executable: No such file or directory|Failed to open .*: Permission denied|references a path below legacy directory" "$log" | grep -qE "mofgw-sync.service|ERROR|Failed"; then
      fail "systemd-analyze verify mofgw-sync.service con errores REALES"
      cat "$log" >&2
    else
      pass "systemd-analyze verify mofgw-sync.service (tolerado: binario %h ausente en sandbox — territorio del canary B4)"
    fi
  fi
}

# ---------------------------------------------------------------------------
# Test 7 (019-006 C4/C5/P12): sync crea units + binario; el resumen final
# reporta el timer y el binario.
# ---------------------------------------------------------------------------
test_sync_creates_files() {
  new_sandbox
  local sync_bin="$SANDBOX/.local/bin/mofgw-sync"
  local sync_service="$SANDBOX/.config/systemd/user/mofgw-sync.service"
  local sync_timer="$SANDBOX/.config/systemd/user/mofgw-sync.timer"

  run_install || { fail "install.sh falló"; return; }

  [[ -f "$sync_bin" ]]     && pass "binario sync existe: $sync_bin"     || fail "binario sync no existe: $sync_bin"
  [[ -f "$sync_service" ]] && pass "unit sync service existe"           || fail "unit sync service no existe"
  [[ -f "$sync_timer" ]]   && pass "unit sync timer existe"             || fail "unit sync timer no existe"

  # Fuente única de verdad (I7): los instalados son byte-exactos al repo.
  if [[ -f "$sync_service" ]] && cmp -s "$REPO_ROOT/scripts/systemd/mofgw-sync.service" "$sync_service"; then
    pass "mofgw-sync.service instalado == commiteado"
  else
    fail "mofgw-sync.service instalado != commiteado"
  fi
  if [[ -f "$sync_timer" ]] && cmp -s "$REPO_ROOT/scripts/systemd/mofgw-sync.timer" "$sync_timer"; then
    pass "mofgw-sync.timer instalado == commiteado"
  else
    fail "mofgw-sync.timer instalado != commiteado"
  fi

  # P12: el resumen final reporta el timer y el binario.
  if grep -q "mofgw-sync.timer" "$SANDBOX/install.log" && grep -q "$sync_bin" "$SANDBOX/install.log"; then
    pass "resumen final reporta timer + binario sync"
  else
    fail "resumen final no reporta timer/binario sync"
  fi
}

# ---------------------------------------------------------------------------
# Test 8 (019-006 C4): MOFGW_SYNC_BIN_SRC copia el prebuilt sin build.
# ---------------------------------------------------------------------------
test_sync_bin_from_src() {
  new_sandbox
  local sync_bin="$SANDBOX/.local/bin/mofgw-sync"
  local stub="$SANDBOX/sync-prebuilt"

  printf '#!/bin/sh\necho sync-stub\n' >"$stub"
  chmod 755 "$stub"

  ( cd "$REPO_ROOT" \
      && MOFGW_HOME="$SANDBOX" MOFGW_SKIP_SYSTEMCTL=1 MOFGW_SYNC_BIN_SRC="$stub" \
           "$INSTALL_SCRIPT" >"$SANDBOX/install.log" 2>&1 ) \
    || { fail "install.sh con MOFGW_SYNC_BIN_SRC falló"; tail -8 "$SANDBOX/install.log" >&2; return; }

  if [[ -f "$sync_bin" ]] && cmp -s "$stub" "$sync_bin"; then
    pass "MOFGW_SYNC_BIN_SRC copiado byte-exacto"
  else
    fail "MOFGW_SYNC_BIN_SRC no copiado byte-exacto"
  fi
}

# ---------------------------------------------------------------------------
# Test 9 (019-006 C4): backup-on-overwrite universal.
# ---------------------------------------------------------------------------
test_sync_units_backup_on_overwrite() {
  new_sandbox
  local udir="$SANDBOX/.config/systemd/user"
  mkdir -p "$udir"
  local diverged_server="$udir/mofgw.service"
  local diverged_service="$udir/mofgw-sync.service"
  local diverged_timer="$udir/mofgw-sync.timer"

  # Pre-siembra units divergidos (incluido el service del server — P4).
  printf '# unit del operador (divergida)\n' >"$diverged_server"
  printf '# sync service del operador (divergido)\n' >"$diverged_service"
  printf '# sync timer del operador (divergido)\n' >"$diverged_timer"
  local prev_server prev_service prev_timer
  prev_server="$(cat "$diverged_server")"
  prev_service="$(cat "$diverged_service")"
  prev_timer="$(cat "$diverged_timer")"

  run_install || { fail "install.sh falló"; return; }

  local bak
  for u in mofgw.service mofgw-sync.service mofgw-sync.timer; do
    bak="$(compgen -G "$udir/${u}.bak.*" | head -1)"
    if [[ -n "$bak" ]]; then
      pass "backup $u.bak.* creado"
    else
      fail "backup $u.bak.* NO creado (I3: jamás pisar sin backup)"
    fi
  done

  # Byte-exacto del contenido previo (I3).
  if [[ "$(cat "$(compgen -G "$udir/mofgw.service.bak.*" | head -1)")" == "$prev_server" ]]; then
    pass "contenido previo de mofgw.service byte-exacto en backup"
  else
    fail "backup de mofgw.service perdió contenido"
  fi
  if [[ "$(cat "$(compgen -G "$udir/mofgw-sync.service.bak.*" | head -1)")" == "$prev_service" ]]; then
    pass "contenido previo de mofgw-sync.service byte-exacto en backup"
  else
    fail "backup de mofgw-sync.service perdió contenido"
  fi
  if [[ "$(cat "$(compgen -G "$udir/mofgw-sync.timer.bak.*" | head -1)")" == "$prev_timer" ]]; then
    pass "contenido previo de mofgw-sync.timer byte-exacto en backup"
  else
    fail "backup de mofgw-sync.timer perdió contenido"
  fi

  # Idempotencia: segunda corrida no acumula backups nuevos.
  local n_before n_after
  n_before="$(compgen -G "$udir/*.bak.*" | wc -l | tr -d ' ')"
  run_install || { fail "segunda corrida falló"; return; }
  n_after="$(compgen -G "$udir/*.bak.*" | wc -l | tr -d ' ')"
  if [[ "$n_before" == "$n_after" ]]; then
    pass "sin backups nuevos en re-corrida idempotente (n=$n_after)"
  else
    fail "backups acumulados en re-corrida ($n_before → $n_after)"
  fi
}

# ---------------------------------------------------------------------------
# Test 10 (019-006 C5): permisos 755 del binario sync.
# ---------------------------------------------------------------------------
test_sync_bin_permissions() {
  new_sandbox
  local sync_bin="$SANDBOX/.local/bin/mofgw-sync"

  run_install || { fail "install.sh falló"; return; }

  local mode
  mode="$(stat -c '%a' "$sync_bin" 2>/dev/null || printf '000')"
  if [[ "$mode" =~ ^7[0-7][0-7]$ ]]; then
    pass "permisos del binario sync = $mode (700+)"
  else
    fail "permisos del binario sync = $mode (esperado 755/700+)"
  fi
}

# ---------------------------------------------------------------------------
# Test 11 (019-006 C7/P6): si start_service muere, el timer NO se agenda.
# ---------------------------------------------------------------------------
test_sync_timer_requires_healthy_server() {
  new_sandbox
  # Listener ajeno en 3369: el fake is-active dice "no activo" → la rama
  # port_in_use muere (proceso ajeno, jamás se toca — solo lectura localhost).
  python3 -m http.server 3369 --bind 127.0.0.1 >/dev/null 2>&1 &
  local listener=$!
  sleep 1

  if run_install_noskip; then
    kill "$listener" 2>/dev/null || true
    fail "install.sh exit 0 con puerto ocupado (esperaba die de start_service)"
    return
  fi
  kill "$listener" 2>/dev/null || true

  pass "install.sh murió con el puerto ocupado (start_service fail-loud)"
  # P6: sin enable del timer (el fake lo demuestra — el log no tiene enable
  # de mofgw-sync.timer).
  if grep -q "enable.*mofgw-sync.timer" "$SANDBOX/systemctl.calls" 2>/dev/null; then
    fail "el timer se habilitó pese al install fallido (P6: NO agendar sobre install fallido)"
  else
    pass "cero enable de mofgw-sync.timer tras install fallido (P6)"
  fi
}

# ---------------------------------------------------------------------------
# Test 12 (019-006 C8/P11): --uninstall borra units+bin del sync, preserva .bak.
# ---------------------------------------------------------------------------
test_sync_uninstall() {
  new_sandbox
  local sync_bin="$SANDBOX/.local/bin/mofgw-sync"
  local udir="$SANDBOX/.config/systemd/user"

  run_install || { fail "install.sh falló"; return; }

  # Sembrar un .bak antes del uninstall (debe sobrevivir).
  printf 'contenido-previo' >"$udir/mofgw-sync.service.bak.20000101000000"

  if run_install --uninstall; then
    pass "uninstall exit 0"
  else
    fail "uninstall exit != 0"; tail -8 "$SANDBOX/install.log" >&2; return
  fi

  [[ ! -e "$udir/mofgw-sync.service" ]] && pass "sync service eliminado" || fail "sync service sigue existiendo"
  [[ ! -e "$udir/mofgw-sync.timer" ]]   && pass "sync timer eliminado"   || fail "sync timer sigue existiendo"
  [[ ! -e "$sync_bin" ]]                && pass "binario sync eliminado" || fail "binario sync sigue existiendo"
  if [[ -f "$udir/mofgw-sync.service.bak.20000101000000" ]]; then
    pass ".bak.* de units preservado en uninstall (I3)"
  else
    fail ".bak.* borrado por uninstall (I3: inmortales)"
  fi
}

# ---------------------------------------------------------------------------
main() {
  say "== Harness 005-003-systemd (sandbox MOFGW_SKIP_SYSTEMCTL=1) =="
  for t in test_install_creates_files \
           test_unit_passes_verify \
           test_unit_passes_verify_all \
           test_idempotent \
           test_uninstall \
           test_respects_existing_config \
           test_bin_permissions \
           test_sync_creates_files \
           test_sync_bin_from_src \
           test_sync_units_backup_on_overwrite \
           test_sync_bin_permissions \
           test_sync_timer_requires_healthy_server \
           test_sync_uninstall; do
    say "--- $t"
    "$t"
  done
  say ""
  say "Resultado: $PASS PASS, $FAIL FAIL"
  [[ "$FAIL" == 0 ]]
}
main
