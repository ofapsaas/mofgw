#!/usr/bin/env python3
"""burn-weekly.py — reporte semanal de burn de mofgw desde daily.jsonl.

Agrega los deltas por intervalo de burn-daily.py (hb/.cache/mofgw-burn/daily.jsonl)
a un resumen por día y por cliente para una ventana semanal.

Uso:
  python3 projects/mofgw/scripts/burn-weekly.py               # últimos 7 días
  python3 projects/mofgw/scripts/burn-weekly.py --days 14     # ventana custom
  python3 projects/mofgw/scripts/burn-weekly.py --json        # solo JSON

Output: reporte markdown a stdout (o JSON con --json). Read-only.
Notas de cobertura:
  - Solo cuenta delta_usd >= 0 (deltas negativos por reinicio de state.json
    no existen: burn-daily ya los excluye). Reinicios de mofgw pueden
    subestimar burn (contadores se resetean a cero si no hay persistencia).
  - Días sin entradas = sin captura (ej: baseline 17:31 del 12 Sep 2026;
    días completos desde el 13 Sep 2026).
"""
import json
import sys
import datetime
from collections import defaultdict
from pathlib import Path

CACHE = Path.home() / 'clawd/hb/.cache/mofgw-burn'
DAILY = CACHE / 'daily.jsonl'

# Mapa de normalización de claves de cliente (anonimización). Único lugar del
# script donde viven los nombres reales: son las claves exactas que emite el
# tracker en daily.jsonl vivo; sin ellas el desglose por cliente no sería
# estable.
_NORM = {
    'ofap-opencode': 'cliente-a-opencode',
    'ofap-openclaw': 'cliente-a-openclaw',
    'ofap': 'cliente-a',
    'prizzodrgit': 'cliente-c',
}


def norm(client):
    """Clave de cliente de daily.jsonl → identificador anónimo 'cliente-*'.

    Clientes desconocidos pasan tal cual; no se inventan nombres.
    """
    if client in _NORM:
        return _NORM[client]
    if client.startswith('ofap-'):
        return 'cliente-a-' + client[len('ofap-'):]
    if client.startswith('blovx-'):
        return 'cliente-b-' + client[len('blovx-'):]
    if client == 'blovx':
        return 'cliente-b'
    return client


def load_entries():
    if not DAILY.exists():
        return []
    out = []
    for line in DAILY.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return out


def main():
    args = sys.argv[1:]
    as_json = '--json' in args
    days = 7
    if '--days' in args:
        days = int(args[args.index('--days') + 1])

    now = datetime.datetime.now(datetime.timezone.utc).astimezone()
    today = now.date()
    start = today - datetime.timedelta(days=days - 1)

    entries = [e for e in load_entries() if 'ts' in e]
    window = []
    for e in entries:
        try:
            d = datetime.datetime.fromisoformat(e['ts']).date()
        except ValueError:
            continue
        if start <= d <= today:
            window.append((d, e))

    per_day = defaultdict(float)
    per_day_client = defaultdict(lambda: defaultdict(float))
    for d, e in window:
        delta = e.get('delta_usd') or {}
        for c, v in delta.items():
            c = norm(c)  # frontera: el desglose solo ve nombres 'cliente-*'
            per_day[str(d)] += float(v)
            per_day_client[str(d)][c] += float(v)

    per_client_total = defaultdict(float)
    for d in per_day_client:
        for c, v in per_day_client[d].items():
            per_client_total[c] += v

    total = sum(per_day.values())
    covered = len(per_day)
    run_rate = (total / covered * days) if covered else 0.0

    result = {
        'generated_at': now.isoformat(timespec='seconds'),
        'window': {'start': str(start), 'end': str(today), 'days': days},
        'coverage': {'days_with_data': covered, 'days_expected': days},
        'total_usd': round(total, 4),
        'run_rate_window_usd': round(run_rate, 2),
        'per_day_usd': {d: round(v, 4) for d, v in sorted(per_day.items())},
        'per_day_clients': {d: {c: round(v, 4) for c, v in cl.items()}
                            for d, cl in sorted(per_day_client.items())},
        'per_client_total_usd': {c: round(v, 4) for c, v in
                                 sorted(per_client_total.items(),
                                        key=lambda kv: -kv[1])},
    }

    if as_json:
        print(json.dumps(result, indent=2))
        return

    lines = []
    lines.append('# 🔥 Burn Weekly mofgw — %s → %s' % (start, today))
    lines.append('')
    lines.append('- **Total ventana:** $%.2f (%d/%d días con captura)' %
                 (total, covered, days))
    lines.append('- **Run-rate proyectado:** $%.2f/%d días' % (run_rate, days))
    lines.append('')
    lines.append('| Día | USD | desglose |')
    lines.append('|-----|-----|----------|')
    for d in sorted(per_day):
        cl = per_day_client[d]
        brk = ', '.join('%s $%.2f' % (c, v) for c, v in
                        sorted(cl.items(), key=lambda kv: -kv[1]))
        lines.append('| %s | $%.4f | %s |' % (d, per_day[d], brk))
    if not per_day:
        lines.append('| (sin capturas) | — | — |')
    lines.append('')
    if per_client_total:
        lines.append('**Por cliente:** ' + ', '.join(
            '%s $%.2f' % (c, v) for c, v in
            sorted(per_client_total.items(), key=lambda kv: -kv[1])))
    lines.append('')
    component_section(lines, days)
    print('\n'.join(lines))


def component_section(lines, days):
    """Apéndice: burn por componente (Bet G, S42) vía burn_component.

    Best-effort: si el módulo o sus fuentes fallan, la sección se omite
    con una nota (el reporte semanal no debe romperse por el addon).
    """
    try:
        import importlib.util
        mod_path = Path(__file__).resolve().parent / 'burn-component.py'
        spec = importlib.util.spec_from_file_location('burn_component', mod_path)
        bc = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(bc)
    except Exception as exc:  # noqa: BLE001 — addon informativo
        lines.append('> (burn por componente omitido: %s)' % exc)
        return
    try:
        intervals = bc.load_burn_intervals(days)
        windows = bc.load_cron_windows(days)
        comp = bc.attribute(intervals, windows)
    except Exception as exc:  # noqa: BLE001 — addon informativo
        lines.append('> (burn por componente omitido: %s)' % exc)
        return
    total = sum(comp.values())
    lines.append('')
    lines.append('## 🔬 Burn por componente (misma ventana)')
    lines.append('')
    lines.append('| Componente | USD | % |')
    lines.append('|------------|-----|---|')
    for k, v in sorted(comp.items(), key=lambda x: -x[1]):
        pct = ('%.1f%%' % (100 * v / total)) if total else '—'
        lines.append('| %s | $%.2f | %s |' % (k, v, pct))
    lines.append('| **TOTAL propio** | **$%.2f** | 100%% |' % total)
    lines.append('')
    lines.append('_Aprox v1: split cliente-a-openclaw por arranque de cron en el '
                 'intervalo (~1h); guardias no separables de heartbeat; '
                 'excluye cliente-b-*. Detalle: burn-component.py._')


if __name__ == '__main__':
    main()
