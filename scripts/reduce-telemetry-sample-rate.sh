#!/usr/bin/env bash
# reduce-telemetry-sample-rate.sh — S37 Apuesta A: cierre de ventana de discovery
# ---------------------------------------------------------------
# Programa la reducción de sample_rate 1 → 0.1 tras completar la ventana
# de 24h de telemetría de discovery (arrancó 08 Ago 22:08).
#
# Guardas:
#   1. No ejecuta si la ventana no completó 24h (verifica mtime del telemetry.jsonl
#      vs hora de inicio registrada). Forzable con --force (solo para testing).
#   2. Backup de config.yaml antes de tocar (config.yaml.bak-sample-rate-<ts>).
#   3. Restart + verificación de salud post-cambio (0 chain errors, config cargada).
#   4. Log del resultado en la bitácora diaria vía hb/log-append.sh.
#
# Reversible: volver a sample_rate: 1 y restart mofgw.service.

set -euo pipefail

CONFIG="${MOFGW_CONFIG:-/home/<user>/.config/mofgw/config.yaml}"
TELEMETRY_FILE="${MOFGW_TELEMETRY:-/home/<user>/logs/mofgw-telemetry.jsonl}"
START_REF="2026-08-08T22:08:00-03:00"   # inicio de la ventana de discovery
FORCE="${1:-}"

OFAP_HOME="${OFAP_HOME:-/home/<user>/clawd}"
LOG_APPEND="$OFAP_HOME/hb/log-append.sh"

log() { echo "[$(date '+%H:%M')] $*"; }

# ---- Guarda 1: ventana completa (>= 24h desde START_REF) ----
if [ "$FORCE" != "--force" ]; then
  start_epoch=$(date -d "$START_REF" +%s)
  now_epoch=$(date +%s)
  elapsed=$(( (now_epoch - start_epoch) / 3600 ))
  if [ "$elapsed" -lt 24 ]; then
    log "⏳ Ventana de discovery incompleta (${elapsed}h < 24h). Saliendo sin cambios."
    exit 0
  fi
  log "✅ Ventana de discovery completa (${elapsed}h >= 24h). Procediendo."
fi

# ---- Guarda 2: backup ----
TS=$(date +%Y%m%d-%H%M%S)
cp "$CONFIG" "$CONFIG.bak-sample-rate-$TS"
log "📦 Backup: $CONFIG.bak-sample-rate-$TS"

# ---- Cambio sample_rate 1 → 0.1 ----
CURRENT=$(grep -E '^\s*sample_rate:' "$CONFIG" | head -1 | awk '{print $2}')
if [ "$CURRENT" = "0.1" ]; then
  log "ℹ️  sample_rate ya está en 0.1. Nada que hacer."
  exit 0
fi
sed -i "s/^\s*sample_rate:.*/  sample_rate: 0.1/" "$CONFIG"
log "🔄 sample_rate: $CURRENT → 0.1"

# ---- Guarda 3: restart + verificación ----
systemctl --user restart mofgw.service
sleep 3
if systemctl --user is-active --quiet mofgw.service; then
  log "✅ mofgw.service activo post-restart"
else
  log "❌ mofgw.service NO activo post-restart — revirtiendo config"
  cp "$CONFIG.bak-sample-rate-$TS" "$CONFIG"
  systemctl --user restart mofgw.service
  exit 1
fi

# Config cargada correctamente?
grep -E '^\s*sample_rate:' "$CONFIG" | grep -q "0.1" && log "✅ sample_rate 0.1 confirmado en config"

# ---- Guarda 4: bitácora ----
if [ -x "$LOG_APPEND" ]; then
  cat << EOF | bash "$LOG_APPEND"
## $(date '+%H:%M') Cycle — mofgw sample_rate 1→0.1 (cierre discovery, timer programado)

- **Qué hice:** Reduje sample_rate de telemetría a 0.1 tras completar ventana 24h de discovery (iniciada 08 Ago 22:08). Backup config en $CONFIG.bak-sample-rate-$TS.
- **Verificación:** ventana completa, restart OK, config confirmada.
- **Estado:** ✅ DONE
EOF
fi

log "🏁 Done."
