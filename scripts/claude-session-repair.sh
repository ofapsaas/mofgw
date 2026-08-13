#!/bin/sh
# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# claude-session-repair.sh — repara las sesiones claude duplicadas de un
# cliente de mofgw (EPIC-013).
#
# Cada cliente de mofgw usa una sesión NOMBRADA de claude (nombre = clientID).
# Si por error se acumulan varias sesiones claude con el mismo nombre (p.ej.
# se recreó la sesión tras una limpieza, o pruebas manuales), `--resume
# <nombre>` falla con "matches 2 sessions" (no hay picker en modo -p). Este
# script limpia los duplicados de los directorios de proyecto claude de ese
# cliente.
#
# Uso:
#   claude-session-repair.sh <clientID> [--keep-newest|--fresh]
#
#   <clientID>     id del cliente mofgw (p.ej. ofap-opencode). El nombre de
#                  sesión claude es el clientID saneado a [a-zA-Z0-9_-].
#   --keep-newest  (default) conserva la sesión .jsonl más reciente de cada
#                  directorio de proyecto y borra el resto. Preserva la
#                  conversación más reciente.
#   --fresh        borra TODAS las sesiones .jsonl del cliente. mofgw
#                  recrea una sesión fresca única con -n en el próximo
#                  request (el motor reintenta con creación cuando --resume
#                  reporta "no conversation found").
#
# Solo toca los directorios de proyecto claude del cliente indicado
# (busca en ~/.claude/projects/ los que contienen el patrón clients-<id>).
# No afecta a otros clientes.

set -u

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "uso: $0 <clientID> [--keep-newest|--fresh]" >&2
  exit 2
fi

CLIENT="$1"
MODE="${2:---keep-newest}"

# Sanitizar el clientID a [a-zA-Z0-9_-] (igual que sessionName del adapter)
# y evitar path traversal.
SAFE=$(printf '%s' "$CLIENT" | tr -c 'A-Za-z0-9_-' '-')
SAFE=$(printf '%s' "$SAFE" | sed 's/^-\+//; s/-\+$//')
if [ -z "$SAFE" ]; then
  echo "error: clientID inválido" >&2
  exit 2
fi
if [ "$SAFE" != "$CLIENT" ]; then
  echo "aviso: clientID saneado '$CLIENT' -> '$SAFE'" >&2
fi

BASE="$HOME/.claude/projects"
if [ ! -d "$BASE" ]; then
  echo "no existe $BASE — no hay sesiones de claude"
  exit 0
fi

# Buscar los directorios de proyecto de este cliente (los de mofgw contienen
# el patrón .../clients/<id> en el cwd que claude codifica en el nombre).
PROJS=$(find "$BASE" -maxdepth 1 -type d -name "*clients-$SAFE" 2>/dev/null)
if [ -z "$PROJS" ]; then
  echo "no hay directorios de proyecto claude para '$SAFE' (patrón *clients-$SAFE en $BASE)"
  exit 0
fi

for PROJ in $PROJS; do
  echo "=== proyecto: $PROJ ==="
  # shellcheck disable=SC2012
  ls -lt "$PROJ"/*.jsonl 2>/dev/null || { echo "  (sin archivos .jsonl)"; continue; }

  case "$MODE" in
    --keep-newest)
      OLD=$(ls -t "$PROJ"/*.jsonl 2>/dev/null | tail -n +2)
      if [ -z "$OLD" ]; then
        echo "  solo una sesión — sin duplicados, nada que borrar"
      else
        echo "  conservando la más reciente, borrando:"
        printf '    %s\n' $OLD
        # shellcheck disable=SC2086
        rm $OLD
      fi
      ;;
    --fresh)
      echo "  borrando todas las sesiones .jsonl:"
      printf '    %s\n' "$PROJ"/*.jsonl
      rm "$PROJ"/*.jsonl
      ;;
    *)
      echo "modo desconocido: $MODE (usa --keep-newest o --fresh)" >&2
      exit 2
      ;;
  esac

  echo "  restantes:"
  ls -t "$PROJ"/*.jsonl 2>/dev/null | sed 's/^/    /' || echo "    (sin sesiones .jsonl)"
done

echo "listo. mofgw recrea la sesión en el próximo request del cliente."