#!/usr/bin/env python3
"""burn-daily.py — tracker de burn diario de mofgw via diff de state.json.

mofgw (/metrics y state.json) solo expone contadores CUMULATIVOS: no hay ventana
diaria. Este script mantiene snapshots en hb/.cache/mofgw-burn/ y calcula:
  - burn de HOY (desde el rollover 00:00 local) por cliente
  - delta desde la última corrida (intervalo)
Output: JSON en stdout + append a daily.jsonl (histórico por día).

Uso:
  python3 projects/mofgw/scripts/burn-daily.py            # status + delta
  python3 projects/mofgw/scripts/burn-daily.py --init     # baseline sin delta

Read-only respecto de mofgw (nunca escribe en state.json).
Fuente de datos: ~/.config/mofgw/state.json (cost_usd por client|provider|model).
"""
import json
import sys
import datetime
from pathlib import Path

STATE = Path.home() / '.config/mofgw/state.json'
CACHE = Path.home() / 'clawd/hb/.cache/mofgw-burn'
SNAP = CACHE / 'state.json'
DAILY = CACHE / 'daily.jsonl'


def current_totals():
    s = json.loads(STATE.read_text())
    saved_at = s.get('saved_at')
    out = {}
    for k, v in (s.get('cost_usd') or {}).items():
        c = k.split('|')[0]
        out[c] = out.get(c, 0.0) + float(v)
    return saved_at, out


def main():
    init = '--init' in sys.argv
    saved_at, cur = current_totals()
    now = datetime.datetime.now(datetime.timezone.utc).astimezone()
    today = now.strftime('%Y-%m-%d')
    CACHE.mkdir(parents=True, exist_ok=True)

    prev = None
    if SNAP.exists():
        try:
            prev = json.loads(SNAP.read_text())
        except Exception:
            prev = None

    result = {
        'ts': now.isoformat(timespec='seconds'),
        'saved_at': saved_at,
        'totals_usd': cur,
        'total_usd': round(sum(cur.values()), 4),
    }

    if prev is None or init:
        # baseline: burn_today = 0 por definición desde esta corrida
        result['note'] = 'baseline establecido — burn_today cuenta desde esta corrida'
        daily_totals = cur
    else:
        prev_tot = prev.get('totals_usd', {})
        delta = {c: round(cur.get(c, 0) - prev_tot.get(c, 0), 6)
                 for c in cur if cur.get(c, 0) > prev_tot.get(c, 0) + 1e-9}
        result['delta_since_last_usd'] = delta
        result['delta_since_last_total'] = round(sum(delta.values()), 4)

        rollover = prev.get('daily_date') != today
        base = prev_tot if rollover else prev.get('daily_totals', prev_tot)
        daily = {c: round(cur.get(c, 0) - base.get(c, 0), 4)
                 for c in cur if cur.get(c, 0) > base.get(c, 0) + 1e-9}
        result['daily_date'] = today
        result['burn_today_usd'] = daily
        result['burn_today_total'] = round(sum(daily.values()), 4)

        if delta:
            line = {'ts': result['ts'], 'saved_at': saved_at,
                    'delta_usd': delta, 'burn_today_usd': daily}
            DAILY.parent.mkdir(parents=True, exist_ok=True)
            with DAILY.open('a') as f:
                f.write(json.dumps(line) + '\n')
        daily_totals = cur if rollover else prev.get('daily_totals', prev_tot)

    SNAP.write_text(json.dumps({
        'daily_date': today,
        'daily_totals': daily_totals,
        'totals_usd': cur,
        'ts': result['ts'],
    }))
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
