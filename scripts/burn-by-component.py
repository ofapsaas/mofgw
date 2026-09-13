#!/usr/bin/env python3
"""burn-by-component.py — Bet G (Pablo 13 Sep 08:45): atribuir burn de mofgw por componente.

Consume los deltas timestamped de hb/.cache/mofgw-burn/daily.jsonl (generados por
burn-daily.py) y los headers de ciclo del daily log (hb/YYMMDD.md) para atribuir
cada ventana de delta a un componente:

  - workers_opencode : cliente ofap-opencode (rondas de workers OpenCode: odoo-go,
                       mofgw, cdad) — el cliente ya separa este bucket.
  - crons            : entradas '## HH:MM Cron' del log dentro de la ventana, o
                       cron programado conocido (Obs-Radar 09:00 ART).
  - heartbeat        : ventana con header '## HH:MM Cycle' y sin evento cron.
  - openclaw_otros   : delta de ofap-openclaw sin ciclo ni cron en la ventana
                       (background poll, guardias, spills de ventana anterior).
  - clientes externos: blovx-openclaw, zot, etc. → atribuidos a su cliente.

Prioridad por ventana: cron explícito > cron conocido (radar 09:00) > heartbeat > otros.

Output: JSON en stdout (--json) o resumen markdown. Read-only.

Uso:
  python3 projects/mofgw/scripts/burn-by-component.py                 # markdown
  python3 projects/mofgw/scripts/burn-by-component.py --json          # JSON
  python3 projects/mofgw/scripts/burn-by-component.py --last 3d       # ventana
"""
import json
import re
import sys
import datetime
from collections import defaultdict
from pathlib import Path

HOME = Path.home()
DAILY = HOME / 'clawd/hb/.cache/mofgw-burn/daily.jsonl'
HB = HOME / 'clawd/hb'

# Crons gateway conocidos con horario ART fijo (nombre, HH, tolerancia_min)
KNOWN_CRONS = [('radar', 9, 0)]  # Obs-Radar 09:00 ART

HEADER_RE = re.compile(r'^## (\d{1,2}):(\d{2}) (Cycle|Cron|Corrección)', re.M)


def load_deltas():
    out = []
    if not DAILY.exists():
        return out
    for line in DAILY.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return out


def cycle_events():
    """Mapa (date_str -> set de (HH,MM,kind)) desde los logs hb/YYMMDD.md."""
    events = {}
    for log in sorted(HOME.glob('clawd/hb/2[0-9][0-9][0-9][0-9][0-9].md')):
        digits = log.stem  # YYMMDD, ej: 260912
        if not re.fullmatch(r'2\d{5}', digits):
            continue
        date = f'20{digits[:2]}-{digits[2:4]}-{digits[4:6]}'
        found = []
        for mm in HEADER_RE.finditer(log.read_text()):
            found.append((int(mm[1]), int(mm[2]), mm[3].lower()))
        if found:
            events[date] = found
    return events


def classify_window(ts_from, ts_to, events_for_day, events_prev_day):
    """Resuelve el componente dominante de la ventana [ts_from, ts_to)."""
    for ev in ('cron', 'corrección'):
        hits = [t for t in events_for_day if t[2] == ev and ts_from.time() <= datetime.time(t[0], t[1]) <= ts_to.time()]
        if hits:
            return 'crons'
    # cron conocido: Obs-Radar 09:00 ART con tolerancia ±30min
    if any(t[0] == 9 and abs(t[1]) <= 30 for t in events_for_day if t[2] in ('cycle', 'cron')) and \
       datetime.time(8, 30) <= ts_to.time() <= datetime.time(9, 30):
        return 'crons'
    if any(t[2] == 'cycle' and ts_from.time() <= datetime.time(t[0], t[1]) <= ts_to.time()
           for t in events_for_day):
        return 'heartbeat'
    return 'openclaw_otros'


def main():
    as_json = '--json' in sys.argv
    deltas = load_deltas()
    events = cycle_events()

    burn = defaultdict(float)
    windows = 0
    for i in range(1, len(deltas)):
        prev, cur = deltas[i - 1], deltas[i]
        t_from = datetime.datetime.fromisoformat(prev['ts'])
        t_to = datetime.datetime.fromisoformat(cur['ts'])
        day = t_to.date().isoformat()
        comp = classify_window(t_from, t_to, events.get(day, []), events.get((t_to - datetime.timedelta(days=1)).date().isoformat(), []))
        for client, usd in cur['delta_usd'].items():
            if client == 'ofap-opencode':
                burn['workers_opencode'] += usd
            elif client.startswith('ofap-openclaw'):
                burn[comp] += usd
            elif client == 'blovx-openclaw' or client == 'blovx':
                burn['cliente_blovx'] += usd
            elif client == 'zot':
                burn['cliente_zot'] += usd
            else:
                burn[f'cliente_{client}'] += usd
        windows += 1

    total = sum(burn.values())
    result = {
        'ts': datetime.datetime.now(datetime.timezone.utc).astimezone().isoformat(timespec='seconds'),
        'ventanas_analizadas': windows,
        'cobertura': {'desde': deltas[0]['ts'], 'hasta': deltas[-1]['ts']} if deltas else None,
        'total_usd': round(total, 4),
        'burn_usd': {k: round(v, 4) for k, v in sorted(burn.items(), key=lambda x: -x[1])},
    }
    if as_json:
        print(json.dumps(result, indent=2))
        return
    print(f"# Burn por componente (deltas {deltas[0]['ts'][:16]} → {deltas[-1]['ts'][:16]})\n")
    print(f"Ventanas analizadas: {windows} · Total: ${total:.2f}\n")
    print("| Componente | USD | % |")
    print("|---|---|---|")
    for k, v in sorted(burn.items(), key=lambda x: -x[1]):
        print(f"| {k} | ${v:.4f} | {100*v/total:.1f}% |")


if __name__ == '__main__':
    main()
