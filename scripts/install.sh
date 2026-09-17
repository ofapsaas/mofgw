#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later
# install.sh — instala mofgw como servicio systemd de usuario (005-003-systemd).
#
# Contrato mínimo:
#   1. Copia el binario mofgw a $MOFGW_BIN_DIR/mofgw (755)
#   2. Instala mofgw.service (unit user) en $MOFGW_UNIT_DIR
#   3. Copia config.example.yaml → config real SOLO si no existe
#   4. Copia el binario mofgw-sync a $MOFGW_BIN_DIR/mofgw-sync (755) [epic 019]
#   5. Instala mofgw-sync.{service,timer} desde scripts/systemd/ [epic 019]
#   6. systemctl --user daemon-reload + enable --now (+ timer)
#   7. Verifica: systemctl --user is-active mofgw + TCP/curl 127.0.0.1:3369
#      (+ list-timers del sync)
#
# Idempotente: re-correr no duplica units ni pisa config existente.
# Units divergidos (reales, del operador) se protegen con
# <unit>.bak.<timestamp> ANTES de sobrescribirlos (D6 de 019-006: re-correr
# install.sh JAMÁS destruye la config del operador).
# --uninstall: stop + disable, borra units y binarios (sync incluido); la
#              config existente se conserva como config.yaml.bak.<timestamp>.
#              Los .bak.* de units jamás se borran.
#
# Seguridad: si el puerto 3369 ya está ocupado por OTRO proceso al instalar,
# el script falla con mensaje claro y NO mata procesos ajenos.
#
# Variables de entorno (también para tests en sandbox):
#   MOFGW_HOME            base para bin/ y .config/ (default: $HOME)
#   MOFGW_BIN_DIR         default $MOFGW_HOME/.local/bin
#   MOFGW_CONFIG_DIR      default $MOFGW_HOME/.config/mofgw
#   MOFGW_UNIT_DIR        default $MOFGW_HOME/.config/systemd/user
#   MOFGW_CONFIG          ruta explícita del config (si apunta a /etc → nivel sistema)
#   MOFGW_SKIP_SYSTEMCTL=1  dry-run: hace los pasos de archivos pero NO toca systemd
#   MOFGW_BIN_SRC         binario prebuilt a copiar (si no, ver install_binary)
#   MOFGW_SYNC_BIN_SRC    binario prebuilt mofgw-sync a copiar (si no, ver
#                           install_sync_binary — espejo de MOFGW_BIN_SRC)
#
# Origen del binario (decisión documentada en install_binary):
#   $MOFGW_BIN_SRC si está set → ./mofgw prebuilt en el repo si existe →
#   go build -o "$MOFGW_BIN_DIR/mofgw" ./cmd/mofgw (construye directo al
#   destino, sin dejar artefacto en el repo).
set -euo pipefail

# ---------------------------------------------------------------------------
# Resolución de rutas
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

MOFGW_HOME="${MOFGW_HOME:-$HOME}"
MOFGW_BIN_DIR="${MOFGW_BIN_DIR:-$MOFGW_HOME/.local/bin}"
MOFGW_CONFIG_DIR="${MOFGW_CONFIG_DIR:-$MOFGW_HOME/.config/mofgw}"
MOFGW_UNIT_DIR="${MOFGW_UNIT_DIR:-$MOFGW_HOME/.config/systemd/user}"
MOFGW_SKIP_SYSTEMCTL="${MOFGW_SKIP_SYSTEMCTL:-0}"
MOFGW_BIN_SRC="${MOFGW_BIN_SRC:-}"
MOFGW_SYNC_BIN_SRC="${MOFGW_SYNC_BIN_SRC:-}"

BIN="$MOFGW_BIN_DIR/mofgw"
UNIT="$MOFGW_UNIT_DIR/mofgw.service"
EXAMPLE_CONFIG="$REPO_ROOT/config.example.yaml"
CONFIG_TARGET="${MOFGW_CONFIG:-$MOFGW_CONFIG_DIR/config.yaml}"

# Epic 019-006: artefactos del sync (fuente única en scripts/systemd/).
SYNC_BIN="$MOFGW_BIN_DIR/mofgw-sync"
SYNC_SERVICE="$MOFGW_UNIT_DIR/mofgw-sync.service"
SYNC_TIMER="$MOFGW_UNIT_DIR/mofgw-sync.timer"
SYNC_SERVICE_SRC="$REPO_ROOT/scripts/systemd/mofgw-sync.service"
SYNC_TIMER_SRC="$REPO_ROOT/scripts/systemd/mofgw-sync.timer"

# ---------------------------------------------------------------------------
# Utilidades
# ---------------------------------------------------------------------------
log() { printf 'install.sh: %s\n' "$*"; }
die() { printf 'install.sh: ERROR: %s\n' "$*" >&2; exit 1; }

# systemctl_user: wrapper que respeta MOFGW_SKIP_SYSTEMCTL (dry-run).
systemctl_user() {
  if [[ "$MOFGW_SKIP_SYSTEMCTL" == "1" ]]; then
    log "dry-run: systemctl --user $*"
    return 0
  fi
  systemctl --user "$@"
}

# port_in_use: true si algo escucha en el puerto dado (ss > netstat > /dev/tcp).
port_in_use() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -ltn 2>/dev/null | grep -qE "[:.]${port}[[:space:]]"
  elif command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | grep -qE "[:.]${port}[[:space:]]"
  else
    (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null && { exec 3>&- 3<&-; return 0; }
    return 1
  fi
}

# ---------------------------------------------------------------------------
# Pasos de instalación
# ---------------------------------------------------------------------------
install_binary() {
  mkdir -p "$MOFGW_BIN_DIR"
  if [[ -n "$MOFGW_BIN_SRC" ]]; then
    [[ -f "$MOFGW_BIN_SRC" ]] || die "MOFGW_BIN_SRC apunta a un archivo inexistente: $MOFGW_BIN_SRC"
    cp "$MOFGW_BIN_SRC" "$BIN"
    log "binario copiado desde MOFGW_BIN_SRC: $MOFGW_BIN_SRC"
  elif [[ -f "$REPO_ROOT/mofgw" ]]; then
    cp "$REPO_ROOT/mofgw" "$BIN"
    log "binario copiado desde $REPO_ROOT/mofgw (prebuilt)"
  else
    log "construyendo binario (go build -o $BIN ./cmd/mofgw)…"
    (cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/mofgw)
    log "binario construido en $BIN"
  fi
  chmod 755 "$BIN"
}

install_config() {
  if [[ -f "$CONFIG_TARGET" ]]; then
    log "config existente preservada: $CONFIG_TARGET"
    return 0
  fi
  [[ -f "$EXAMPLE_CONFIG" ]] || die "no encuentro config.example.yaml en $EXAMPLE_CONFIG"
  if [[ "$CONFIG_TARGET" == /etc/* && "$(id -u)" != 0 ]]; then
    die "MOFGW_CONFIG apunta a nivel sistema ($CONFIG_TARGET) pero no se ejecuta como root"
  fi
  mkdir -p "$(dirname "$CONFIG_TARGET")"
  cp "$EXAMPLE_CONFIG" "$CONFIG_TARGET"
  chmod 600 "$CONFIG_TARGET"
  log "config de ejemplo creada: $CONFIG_TARGET"
}

install_unit() {
  mkdir -p "$MOFGW_UNIT_DIR"
  local tmp
  tmp="$(mktemp "$MOFGW_UNIT_DIR/mofgw.service.XXXXXX")"
  cat >"$tmp" <<EOF
[Unit]
Description=mofgw - MOF Gateway (proxy de IA con fallback transparente)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
TimeoutStartSec=30

[Install]
WantedBy=default.target
EOF
  install_unit_file "$tmp" "$UNIT"
  rm -f "$tmp"
}

# install_unit_file: copia un unit con backup-on-overwrite (D6/P4 de 019-006).
# El trap que corrige: el viejo patrón cmp→mv pisaba sin rescate cualquier
# unit real divergido del operador → hoy produce <unit>.bak.<ts> ANTES de
# instalar. Los .bak son inmortales para install.sh (espejo de config.bak).
install_unit_file() {
  local src="$1" dst="$2"
  mkdir -p "$(dirname "$dst")"
  if [[ ! -f "$dst" ]]; then
    cp "$src" "$dst"
    log "unit instalada: $dst"
    return 0
  fi
  if cmp -s "$src" "$dst"; then
    log "unit ya instalada (contenido idéntico): $dst"
    return 0
  fi
  local ts
  ts="$(date +%Y%m%d%H%M%S)"
  mv "$dst" "${dst}.bak.${ts}"
  log "unit existente divergida, protegida como backup: ${dst}.bak.${ts}"
  cp "$src" "$dst"
  log "unit instalada: $dst"
}

# install_sync_binary: espejo de install_binary para mofgw-sync (019-006 D5).
install_sync_binary() {
  mkdir -p "$MOFGW_BIN_DIR"
  if [[ -n "$MOFGW_SYNC_BIN_SRC" ]]; then
    [[ -f "$MOFGW_SYNC_BIN_SRC" ]] || die "MOFGW_SYNC_BIN_SRC apunta a un archivo inexistente: $MOFGW_SYNC_BIN_SRC"
    cp "$MOFGW_SYNC_BIN_SRC" "$SYNC_BIN"
    log "binario sync copiado desde MOFGW_SYNC_BIN_SRC: $MOFGW_SYNC_BIN_SRC"
  elif [[ -f "$REPO_ROOT/mofgw-sync" ]]; then
    cp "$REPO_ROOT/mofgw-sync" "$SYNC_BIN"
    log "binario sync copiado desde $REPO_ROOT/mofgw-sync (prebuilt)"
  else
    log "construyendo binario sync (go build -o $SYNC_BIN ./cmd/mofgw-sync)…"
    (cd "$REPO_ROOT" && go build -o "$SYNC_BIN" ./cmd/mofgw-sync)
    log "binario sync construido en $SYNC_BIN"
  fi
  chmod 755 "$SYNC_BIN"
}

# install_sync_units: copia los units commiteados (fuente única en
# scripts/systemd/ — I7 de 019-006, jamás heredocs re-generados).
install_sync_units() {
  [[ -f "$SYNC_SERVICE_SRC" ]] || die "no encuentro el unit commiteado: $SYNC_SERVICE_SRC"
  [[ -f "$SYNC_TIMER_SRC" ]] || die "no encuentro el unit commiteado: $SYNC_TIMER_SRC"
  install_unit_file "$SYNC_SERVICE_SRC" "$SYNC_SERVICE"
  install_unit_file "$SYNC_TIMER_SRC" "$SYNC_TIMER"
}

start_service() {
  if [[ "$MOFGW_SKIP_SYSTEMCTL" == "1" ]]; then
    log "dry-run: omitiendo systemd y verificación (MOFGW_SKIP_SYSTEMCTL=1)"
    return 0
  fi

  # No matar nada ajeno: si el servicio ya está activo es re-instalación
  # (el puerto es nuestro); si no, y el puerto está ocupado, fallar claro.
  if systemctl --user is-active mofgw.service >/dev/null 2>&1; then
    log "mofgw.service ya activo (re-instalación): no se chequea el puerto"
  elif port_in_use 3369; then
    die "el puerto 3369 ya está ocupado por otro proceso. No se toca nada ajeno: liberá el puerto antes de instalar."
  fi

  systemctl_user daemon-reload
  systemctl_user enable --now mofgw.service

  local i
  for i in $(seq 1 30); do
    if systemctl --user is-active mofgw.service >/dev/null 2>&1; then break; fi
    sleep 1
  done
  systemctl --user is-active mofgw.service >/dev/null 2>&1 \
    || die "mofgw.service no quedó activo; ver: systemctl --user status mofgw"
  log "servicio activo: $(systemctl --user is-active mofgw.service)"

  if command -v curl >/dev/null 2>&1; then
    local code
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:3369/healthz || true)"
    log "curl http://127.0.0.1:3369/healthz → HTTP $code"
    if [[ "$code" != 2* && "$code" != 3* ]]; then
      die "mofgw no responde en 127.0.0.1:3369 (HTTP $code). Revisá: journalctl --user -u mofgw"
    fi
  else
    if ! (exec 3<>"/dev/tcp/127.0.0.1/3369") 2>/dev/null; then
      die "mofgw no acepta conexiones en 127.0.0.1:3369. Revisá: journalctl --user -u mofgw"
    fi
    exec 3>&- 3<&- || true
  fi
}

# start_sync_timer: agenda el sync DESPUÉS de un server sano (P6 de 019-006:
# si start_service muere, este paso JAMÁS corre — el timer no se agenda
# sobre un install fallido).
start_sync_timer() {
  if [[ "$MOFGW_SKIP_SYSTEMCTL" == "1" ]]; then
    log "dry-run: systemctl --user daemon-reload + enable --now mofgw-sync.timer (MOFGW_SKIP_SYSTEMCTL=1)"
    return 0
  fi
  systemctl_user daemon-reload
  systemctl_user enable --now mofgw-sync.timer
  systemctl --user is-enabled mofgw-sync.timer >/dev/null 2>&1 \
    || die "mofgw-sync.timer no quedó habilitado; ver: systemctl --user status mofgw-sync.timer"
  systemctl --user list-timers mofgw-sync.timer --no-legend 2>/dev/null | grep -q mofgw-sync \
    || die "mofgw-sync.timer no aparece en la agenda; ver: systemctl --user list-timers"
  log "timer activo: $(systemctl --user list-timers mofgw-sync.timer --no-legend | awk '{print $NF}')"
}

# ---------------------------------------------------------------------------
# Desinstalación
# ---------------------------------------------------------------------------
uninstall() {
  log "desinstalando mofgw…"
  if [[ "$MOFGW_SKIP_SYSTEMCTL" != "1" ]]; then
    systemctl --user disable mofgw.service >/dev/null 2>&1 || true
    systemctl --user stop mofgw.service >/dev/null 2>&1 || true
    # El sync se desagenda con tolerancia (el timer puede no existir).
    systemctl --user disable --now mofgw-sync.timer >/dev/null 2>&1 || true
  else
    log "dry-run: omitiendo stop/disable (MOFGW_SKIP_SYSTEMCTL=1)"
  fi
  rm -f "$UNIT"
  rm -f "$BIN"
  # Epic 019-006 (P11): borrar units y binario del sync; los .bak.* de units
  # y de config jamás se borran.
  rm -f "$SYNC_SERVICE" "$SYNC_TIMER" "$SYNC_BIN"
  if [[ -f "$CONFIG_TARGET" ]]; then
    local ts
    ts="$(date +%Y%m%d%H%M%S)"
    mv "$CONFIG_TARGET" "${CONFIG_TARGET}.bak.${ts}"
    log "config preservada: ${CONFIG_TARGET}.bak.${ts}"
  fi
  log "desinstalación completa (config conservada como backup si existía)"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  if [[ "${1:-}" == "--uninstall" ]]; then
    uninstall
    exit 0
  fi
  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
  fi
  [[ $# -eq 0 ]] || die "argumento desconocido: $1 (usá --uninstall o --help)"

  install_binary
  install_config
  install_unit
  install_sync_binary
  install_sync_units
  start_service
  start_sync_timer

  log "mofgw instalado. Unit: $UNIT | Binario: $BIN | Config: $CONFIG_TARGET"
  log "sync:  Unit timer: $SYNC_TIMER | Binario: $SYNC_BIN"
  log "Estado: systemctl --user status mofgw"
  log "Timer:  systemctl --user list-timers mofgw-sync.timer"
}

main "$@"
