#!/usr/bin/env python3
"""burn-component.py — atribución de burn de mofgw por COMPONENTE (Bet G, S42).

Combina dos fuentes:
  1. hb/.cache/mofgw-burn/daily.jsonl — deltas $ timestamped por CLIENTE
     (blovx-openclaw/blovx-opencode, ofap-openclaw, ofap-opencode).
  2. SQLite ~/.openclaw/state/openclaw.sqlite cron_run_logs — runs de gateway
     crons con ts + duration_ms (ventanas de ejecución).

Mapping cliente→componente:
  ofap-opencode   → workers-opencode   (rondas worker/cdad vía sesiones opencode)
  blovx-*         → cliente-blovx      (excluido del análisis de recortes)
  ofap-openclaw   → split por ventanas: si un cron arrancó dentro del intervalo
                    del delta → 'crons-gateway'; resto → 'heartbeat+guardias'
                    (guardias corren DENTRO de ciclos HB; v1 no los separa).

Segunda vista: tokens por cron job (exactos, de cron_run_logs) como cross-check.

Uso:
  python3 projects/mofgw/scripts/burn-component.py              # últimos 7 días
  python3 projects/mofgw/scripts/burn-component.py --days 14
  python3 projects/mofgw/scripts/burn-component.py --json
  python3 projects/mofgw/scripts/burn-component.py --save docs/burn-component-weekly.md
  python3 projects/mofgw/scripts/burn-component.py --selftest   # suite interna

Read-only. Aproximaciones v1 documentadas en el output.
"""
import json
import sqlite3
import sys
import datetime
from collections import defaultdict
from pathlib import Path

DAILY = Path.home() / 'clawd/hb/.cache/mofgw-burn/daily.jsonl'
SQLITE = Path.home() / '.openclaw/state/openclaw.sqlite'

# Clientes excluidos del análisis (no son burn propio)
EXCLUDED_PREFIX = 'blovx'


def load_burn_intervals(days):
    """[(start_ts_dt, end_ts_dt, {client: usd})] desde daily.jsonl."""
    cutoff = datetime.datetime.now(datetime.timezone.utc).astimezone() - \
        datetime.timedelta(days=days)
    out = []
    prev_ts = None
    for line in DAILY.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            e = json.loads(line)
        except json.JSONDecodeError:
            continue
        ts = datetime.datetime.fromisoformat(e['ts'])
        if ts > cutoff:
            out.append((prev_ts, ts, e.get('delta_usd') or {}))
        prev_ts = ts
    return out


def load_cron_windows(days):
    """[(name, start_dt, end_dt)] de cron runs (status ok) en la ventana."""
    cutoff_ms = int((datetime.datetime.now(
        datetime.timezone.utc).timestamp() - days * 86400) * 1000)
    conn = sqlite3.connect(f'file:{SQLITE}?mode=ro', uri=True)
    try:
        rows = conn.execute(
            "SELECT j.name, l.ts, l.duration_ms, COALESCE(l.total_tokens, 0) "
            "FROM cron_run_logs l "
            "JOIN cron_jobs j ON j.job_id = l.job_id "
            "WHERE l.ts > ? AND l.status = 'ok' AND l.duration_ms IS NOT NULL",
            (cutoff_ms,)).fetchall()
    finally:
        conn.close()
    return [(name,
             datetime.datetime.fromtimestamp(ts / 1000,
                                             tz=datetime.timezone.utc).astimezone(),
             datetime.datetime.fromtimestamp((ts + duration) / 1000,
                                             tz=datetime.timezone.utc).astimezone(),
             tokens)
            for name, ts, duration, tokens in rows]


def cron_tokens_by_job(days):
    cutoff_ms = int((datetime.datetime.now(
        datetime.timezone.utc).timestamp() - days * 86400) * 1000)
    conn = sqlite3.connect(f'file:{SQLITE}?mode=ro', uri=True)
    try:
        rows = conn.execute(
            "SELECT j.name, count(*), COALESCE(SUM(l.total_tokens), 0) "
            "FROM cron_run_logs l JOIN cron_jobs j ON j.job_id = l.job_id "
            "WHERE l.ts > ? AND l.status = 'ok' GROUP BY j.name "
            "ORDER BY 3 DESC", (cutoff_ms,)).fetchall()
    finally:
        conn.close()
    return rows


def is_worker_cron(name):
    return name.startswith('worker-cron') or name.startswith('worker-trigger')


def attribute(intervals, cron_windows):
    """Aplica mapping cliente→componente con split por ventanas.

    Solo crons LLM (total_tokens > 0) atribuyen el intervalo: los crons de
    payload-comando (0 tokens) no consumen modelo y no deben robar la
    atribución del intervalo a heartbeat.
    """
    comp = defaultdict(float)
    unattributed = 0.0
    llm_windows = [(n, s) for n, s, _e, tok in cron_windows if tok > 0]
    for start, end, deltas in intervals:
        if start is None:
            # baseline sin intervalo previo: conservador → heartbeat (undercuenta crons)
            for client, usd in deltas.items():
                if not client.startswith(EXCLUDED_PREFIX) and client != 'ofap-opencode':
                    unattributed += usd
            continue
        hit = next((n for n, s in llm_windows if start < s < end), None)
        for client, usd in deltas.items():
            if client.startswith(EXCLUDED_PREFIX):
                continue
            if client == 'ofap-openclaw':
                if hit:
                    comp['worker-trigger-cron' if is_worker_cron(hit)
                         else 'crons-contenido'] += usd
                else:
                    comp['heartbeat+guardias'] += usd
            elif client == 'ofap-opencode':
                comp['workers-opencode'] += usd
            else:
                comp[f'otro:{client}'] += usd
    if unattributed:
        comp['sin-atribuir(baseline)'] = unattributed
    return dict(comp)


def selftest():
    """Suite sintética: mapping, split cron/heartbeat, exclusión blovx."""
    T = datetime.timezone.utc
    mk = lambda d, h, m=0: datetime.datetime(2026, 9, 14, h, m, tzinfo=T) + \
        datetime.timedelta(days=d)
    windows = [('Obs-Radar', mk(0, 9), mk(0, 9, 7), 1000),
               ('worker-cron:mofgw', mk(1, 8, 30), mk(1, 8, 37), 90000),
               ('worker-report-daily', mk(1, 9, 5), mk(1, 9, 6), 0)]
    ivals = [
        # intervalo que contiene el arranque del radar d0 09:00 → cron
        (mk(0, 8, 30), mk(0, 9, 30), {'ofap-openclaw': 1.0,
                                      'ofap-opencode': 2.0,
                                      'blovx-opencode': 5.0}),
        # intervalo sin cron → heartbeat
        (mk(0, 10, 0), mk(0, 11, 0), {'ofap-openclaw': 0.5}),
        # cron de d1 arranca 08:30, mitad del intervalo → cron
        (mk(1, 8, 0), mk(1, 9, 0), {'ofap-openclaw': 1.0}),
        # intervalo fuera de ventana de días → heartbeat
        (mk(1, 12, 0), mk(1, 13, 0), {'ofap-openclaw': 0.25}),
    ]
    got = attribute(ivals, windows)
    assert abs(got['crons-contenido'] - 1.0) < 1e-9, got
    assert abs(got['worker-trigger-cron'] - 1.0) < 1e-9, got
    # worker-report-daily (0 tokens) NO atribuye → ese intervalo es heartbeat
    assert abs(got['heartbeat+guardias'] - 0.75) < 1e-9, got
    assert abs(got['workers-opencode'] - 2.0) < 1e-9, got
    # blovx excluido
    assert not any('blovx' in k for k in got), got
    print(f'--selftest: OK ({len(got)} componentes sintéticos)')


def render(days, comp, total, windows):
    now = datetime.datetime.now().astimezone().strftime('%Y-%m-%d %H:%M')
    lines = [f'## Burn por componente — ventana {days} días (corte {now})\n']
    lines += ['| Componente | USD | % |', '|---|---|---|']
    for k, v in sorted(comp.items(), key=lambda x: -x[1]):
        pct = f'{100 * v / total:.1f}%' if total else '—'
        lines.append(f'| {k} | ${v:.2f} | {pct} |')
    lines.append(f'| **TOTAL propio** | **${total:.2f}** | 100% |')
    lines.append('\nAproximaciones v1: (1) split ofap-openclaw por arranque de cron '
                 'en el intervalo del delta (~1h); (2) guardias corren dentro de '
                 'ciclos HB — no separables aún; (3) excludes blovx-*.')
    lines.append('\n### Cross-check: tokens por cron job (ventana)\n')
    lines += ['| Cron job | runs | tokens |', '|---|---|---|']
    for name, runs, tokens in cron_tokens_by_job(days):
        lines.append(f'| {name} | {runs} | {tokens or 0:,} |')
    return '\n'.join(lines) + '\n'


def main():
    if '--selftest' in sys.argv:
        selftest()
        return
    days = 7
    if '--days' in sys.argv:
        days = int(sys.argv[sys.argv.index('--days') + 1])
    intervals = load_burn_intervals(days)
    windows = load_cron_windows(days)
    comp = attribute(intervals, windows)
    total = sum(comp.values())

    if '--json' in sys.argv:
        print(json.dumps({'window_days': days, 'component_usd': comp,
                          'total_usd': round(total, 4),
                          'intervals': len(intervals),
                          'cron_runs': len(windows)}, indent=2))
        return

    text = render(days, comp, total, windows)
    if '--save' in sys.argv:
        out = Path(sys.argv[sys.argv.index('--save') + 1]).expanduser()
        out.write_text(text)
        print(f'saved: {out} ({len(text)} bytes)')
        return
    print(text, end='')


if __name__ == '__main__':
    main()
