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

# ---------------------------------------------------------------------------
main() {
  say "== Harness 005-003-systemd (sandbox MOFGW_SKIP_SYSTEMCTL=1) =="
  for t in test_install_creates_files \
           test_unit_passes_verify \
           test_idempotent \
           test_uninstall \
           test_respects_existing_config \
           test_bin_permissions; do
    say "--- $t"
    "$t"
  done
  say ""
  say "Resultado: $PASS PASS, $FAIL FAIL"
  [[ "$FAIL" == 0 ]]
}
main
