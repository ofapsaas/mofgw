#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
#
# SPDX-License-Identifier: GPL-3.0-or-later
# fetch-snapshot.sh — regenera cmd/mofgw-sync/snapshot/{api.json,meta.json}
# (019-007-build-snapshot).
#
# Manual, operado por el mantenedor cuando el sync loguee staleness alta
# (warning > 30d, P8 de 019-007). JAMÁS corre en build/CI/tests (I2):
# el build es hermético y jamás necesita red.
#
# Uso: ./scripts/fetch-snapshot.sh
#   MOFGW_SNAPSHOT_URL  fuente del catálogo (default https://models.dev/api.json;
#                       en tests: file:///ruta/api.json)
#   MOFGW_SNAPSHOT_DIR  destino (default <repo>/cmd/mofgw-sync/snapshot)
#
# Salida: api.json byte-idéntico al body + meta.json {fetched_at, sha256,
# source_url}. Body vacío o JSON inválido → exit ≠ 0 sin escribir nada (P14).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

URL="${MOFGW_SNAPSHOT_URL:-https://models.dev/api.json}"
DIR="${MOFGW_SNAPSHOT_DIR:-$REPO_ROOT/cmd/mofgw-sync/snapshot}"

log() { printf 'fetch-snapshot.sh: %s\n' "$*"; }
die() { printf 'fetch-snapshot.sh: ERROR: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl no disponible"
command -v python3 >/dev/null 2>&1 || die "python3 no disponible (validación JSON)"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum no disponible"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

log "descargando $URL…"
curl -fsSL --max-time 180 "$URL" -o "$tmp" || die "descarga fallida (curl exit $?)"

# Valida: JSON parseable + no vacío (P14).
python3 - "$tmp" <<'PYEOF' || die "body vacío o JSON inválido — nada se escribe (P14)"
import json, sys
with open(sys.argv[1]) as f:
    d = json.load(f)
if not d or not isinstance(d, dict):
    sys.exit("body vacío o sin providers")
PYEOF

mkdir -p "$DIR"
sha="$(sha256sum "$tmp" | cut -d' ' -f1)"
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
mv "$tmp" "$DIR/api.json"
trap - EXIT
# F4 (review 019-007): meta.json también atómico (tmp+mv) — un crash entre
# api y meta dejaría par mismatched (api nuevo + meta vieja ⇒ sha mentiroso).
metatmp="$(mktemp)"
printf '{\n  "fetched_at": "%s",\n  "sha256": "%s",\n  "source_url": "%s"\n}\n' \
  "$now" "$sha" "$URL" >"$metatmp"
mv "$metatmp" "$DIR/meta.json"
log "snapshot regenerado: $DIR/api.json + $DIR/meta.json (sha $sha, $now)"
