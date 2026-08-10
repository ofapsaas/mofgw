#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
analyze-sessions.py — Análisis de sesiones de mofgw para optimizar contexto/costo.

Lee state.json y (opcionalmente) telemetry.jsonl de mofgw y produce un reporte
en texto plano con secciones '##' y tablas alineadas manualmente (para terminal).

Uso:
    python3 scripts/analyze-sessions.py [--state PATH] [--telemetry PATH] [--top N] [--verbose]

Solo stdlib (json, collections, sys, datetime, os, argparse, re). Python 3.8+.
"""

import argparse
import json
import os
import re
import sys
from collections import Counter
from datetime import datetime, timedelta, timezone

TZ_ART = timezone(timedelta(hours=-3))
FMT_FULL = "%Y-%m-%d %H:%M:%S"
FMT_COMPACT = "%Y-%m-%d %H:%M"

KNOWN_PART_TYPES = {"text", "image", "tool_use", "tool_result", "thinking", "audio"}
KNOWN_ROLES = {"system", "user", "assistant", "tool", "developer"}


# ---------------------------------------------------------------------------
# Formato
# ---------------------------------------------------------------------------

def fmt_int(value):
    """Entero con separadores de miles; None → '-'. """
    if value is None:
        return "-"
    return "{:,}".format(int(value))


def fmt_float(value, digits=4):
    """Float con separadores de miles; None → '-'. """
    if value is None:
        return "-"
    return "{:,.{d}f}".format(float(value), d=digits)


def pct(part, total):
    """Porcentaje con 1 decimal; total <= 0 → '-'. """
    if not total:
        return "-"
    return "{:.1f}%".format(100.0 * part / total)


def ratio_str(actual, estimated):
    """Ratio actual/estimado (p.ej. '1.08x'); estimado <= 0 → '-'. """
    if not estimated:
        return "-"
    return "{:.2f}x".format(float(actual) / float(estimated))


def ts_to_iso(unix_ts, seconds=True):
    """Unix epoch → ISO legible con zona -03:00; no numérico → '-'. """
    if not isinstance(unix_ts, (int, float)):
        return "-"
    moment = datetime.fromtimestamp(float(unix_ts), tz=TZ_ART)
    pattern = FMT_FULL if seconds else FMT_COMPACT
    return moment.strftime(pattern) + " -03:00"


def window_str(first_ts, last_ts):
    """Ventana first→last compacta y legible. """
    if not isinstance(first_ts, (int, float)) or not isinstance(last_ts, (int, float)):
        return "-"
    return "{} → {}".format(
        ts_to_iso(first_ts, seconds=False), ts_to_iso(last_ts, seconds=False)
    )


_ISO_RE = re.compile(r"^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:\.\d+)?(.*)$")


def normalize_iso(raw):
    """'2026-08-08T22:08:17.010996514-03:00' → '2026-08-08 22:08:17 -03:00'. """
    match = _ISO_RE.match(raw or "")
    if not match:
        return (raw or "")[:40] or "-"
    tz_part = match.group(3).strip()
    if not tz_part:
        return "{} {}".format(match.group(1), match.group(2))
    return "{} {} {}".format(match.group(1), match.group(2), tz_part)


def kv(label, value):
    """Línea 'label: value' con alineación fija. """
    return "  {:<26}{}".format(label + ":", value)


def render_table(headers, rows, aligns=None, indent=2):
    """Tabla en texto plano, columnas alineadas con espacios (sin markdown). """
    if not rows:
        return []
    pad = " " * indent
    widths = [len(header) for header in headers]
    for row in rows:
        for idx, cell in enumerate(row):
            widths[idx] = max(widths[idx], len(cell))
    aligns = aligns or ["l"] * len(headers)
    lines = []
    header_line = pad + "  ".join(
        headers[i].rjust(widths[i]) if aligns[i] == "r" else headers[i].ljust(widths[i])
        for i in range(len(headers))
    )
    lines.append(header_line)
    lines.append(pad + "  ".join("-" * width for width in widths))
    for row in rows:
        cells = [
            cell.rjust(widths[i]) if aligns[i] == "r" else cell.ljust(widths[i])
            for i, cell in enumerate(row)
        ]
        lines.append(pad + "  ".join(cells))
    return lines


# ---------------------------------------------------------------------------
# Carga y análisis
# ---------------------------------------------------------------------------

def split_client_session(key):
    """'zot|ses_abc' → ('zot', 'ses_abc'); 'zot|' → ('zot', ''); sin '|' → (key, ''). """
    if "|" not in key:
        return key, ""
    client, _, session = key.rpartition("|")
    return client, session


def load_state(path):
    """Carga state.json (una línea gigante es OK para json.load). """
    with open(path, "r", encoding="utf-8") as handle:
        return json.load(handle)


def context_session_records(contexts):
    """(session_id, record) para cada contexto de sesión (sufijo no vacío). """
    records = []
    for key, record in (contexts or {}).items():
        _, session_id = split_client_session(key)
        if session_id:
            records.append((session_id, record))
    return records


def _client_aggregate_records(contexts):
    """Pares (clave, record) de los agregados de cliente (session_id vacío, p.ej. 'zot|'). """
    return [
        (key, record) for key, record in (contexts or {}).items()
        if not split_client_session(key)[1]
    ]


def _aggregation_source_records(contexts):
    """Records fuente de los totales globales de part_types/roles.

    Usa SOLO el/los agregado(s) de cliente (session_id vacío) cuando existen: cada
    agregado ya acumula TODO el tráfico del cliente (con y sin sesión), así que sumar
    además las sesiones individuales duplicaría cada request con sesión (doble conteo).
    Fallback: si no hay agregado de cliente, suma las sesiones individuales. """
    aggregates = _client_aggregate_records(contexts)
    if aggregates:
        return [record for _, record in aggregates]
    return [record for _, record in context_session_records(contexts)]


def context_client_aggregate(contexts):
    """Clave y registro del agregado de cliente con más PromptTokensActual, y cuántos
    agregados de cliente hay (para señalar cuando hay varios). """
    aggregates = _client_aggregate_records(contexts)
    if not aggregates:
        return None, None, 0
    best_key, best_record = max(
        aggregates, key=lambda pair: pair[1].get("PromptTokensActual") or 0
    )
    return best_key, best_record, len(aggregates)


def top_context_sessions(session_records, top_n, by_actual_tokens):
    """Top N sesiones por prompt_tokens_actual (True) o por requests (False). """
    if by_actual_tokens:
        def sort_key(record):
            return record.get("PromptTokensActual") or 0
    else:
        def sort_key(record):
            return record.get("Requests") or 0
    return sorted(
        session_records, key=lambda pair: sort_key(pair[1]), reverse=True
    )[:top_n]


def aggregate_part_types(contexts):
    """Agrega Count/Bytes/EstTokens por tipo de parte sobre el agregado de cliente
    (session_id vacío) cuando existe — es la fuente del total global y evita el doble
    conteo con las sesiones individuales. Fallback: solo sesiones individuales. """
    totals = {}
    for record in _aggregation_source_records(contexts):
        for part_type, data in (record.get("PartTypes") or {}).items():
            name = part_type if part_type in KNOWN_PART_TYPES else "other"
            bucket = totals.setdefault(name, {"count": 0, "bytes": 0, "est_tokens": 0})
            bucket["count"] += data.get("Count", 0)
            bucket["bytes"] += data.get("Bytes", 0)
            bucket["est_tokens"] += data.get("EstTokens", 0)
    return sorted(totals.items(), key=lambda item: item[1]["bytes"], reverse=True)


def aggregate_roles(contexts):
    """Agrega Messages/EstTokens por rol sobre el agregado de cliente (session_id
    vacío) cuando existe — es la fuente del total global y evita el doble conteo con
    las sesiones individuales. Fallback: solo sesiones individuales. """
    totals = {}
    for record in _aggregation_source_records(contexts):
        for role, data in (record.get("Roles") or {}).items():
            name = role if role in KNOWN_ROLES else "other"
            bucket = totals.setdefault(name, {"messages": 0, "est_tokens": 0})
            bucket["messages"] += data.get("Messages", 0)
            bucket["est_tokens"] += data.get("EstTokens", 0)
    return sorted(totals.items(), key=lambda item: item[1]["est_tokens"], reverse=True)


def build_session_records(sessions):
    """Records de la sección 'sessions' ordenados por prompt_tokens desc. """
    records = []
    for key, record in (sessions or {}).items():
        _, session_id = split_client_session(key)
        records.append({
            "session_id": session_id or key,
            "requests": record.get("Requests", 0),
            "prompt": record.get("PromptTokens", 0),
            "completion": record.get("CompletionTokens", record.get("CompletionTok", 0)),
            "total": record.get("TotalTokens", 0),
            "cost": record.get("CostUSD", 0.0),
            "first": record.get("FirstRequestAt"),
            "last": record.get("LastRequestAt"),
        })
    records.sort(key=lambda item: item["prompt"], reverse=True)
    return records


def event_user_agent(event):
    """Primer token de headers.User-Agent ('opencode/1.18.10 ...' → 'opencode/1.18.10'). """
    headers = event.get("headers")
    if not isinstance(headers, dict):
        return "(sin user-agent)"
    user_agent = (headers.get("User-Agent") or "").strip()
    return user_agent.split()[0] if user_agent else "(sin user-agent)"


def event_session_id(event):
    """headers.X-Session-Id o None. """
    headers = event.get("headers")
    if not isinstance(headers, dict):
        return None
    return headers.get("X-Session-Id") or None


def event_time_raw(event):
    """Campo 'time' o 'ts' del evento. """
    return event.get("time") or event.get("ts") or ""


def load_telemetry(path):
    """Lee telemetry.jsonl y devuelve stats; None si el archivo no existe. """
    if not os.path.exists(path):
        return None
    events = []
    parse_errors = 0
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as handle:
            for line in handle:
                stripped = line.strip()
                if not stripped:
                    continue
                try:
                    events.append(json.loads(stripped))
                except json.JSONDecodeError:
                    parse_errors += 1
    except OSError as exc:
        return {"error": str(exc)}

    session_ids = [event_session_id(event) for event in events]
    timestamps = [event_time_raw(event) for event in events]
    timestamps = [timestamp for timestamp in timestamps if timestamp]
    return {
        "events": len(events),
        "parse_errors": parse_errors,
        "user_agents": Counter(event_user_agent(event) for event in events),
        "session_counter": Counter(session_id for session_id in session_ids if session_id),
        "with_session": sum(1 for session_id in session_ids if session_id),
        "without_session": len(events) - sum(1 for session_id in session_ids if session_id),
        "first_ts": min(timestamps) if timestamps else None,
        "last_ts": max(timestamps) if timestamps else None,
    }


# ---------------------------------------------------------------------------
# Secciones del reporte
# ---------------------------------------------------------------------------

def section_summary(state):
    lines = []
    lines.append(kv("version", state.get("version", "-")))
    saved_at = state.get("saved_at")
    lines.append(kv("saved_at", "{} (unix {})".format(ts_to_iso(saved_at), fmt_int(saved_at))))
    lines.append(kv("requests", fmt_int(state.get("requests"))))
    lines.append(kv("streams", fmt_int(state.get("streams"))))
    lines.append(kv("errors", fmt_int(state.get("errors"))))
    lines.append(kv("failovers", fmt_int(state.get("failovers"))))
    lines.append(kv("cooldown_hits", fmt_int(state.get("cooldown_hits"))))
    return lines


def context_table_rows(top_records):
    rows = []
    for session_id, record in top_records:
        rows.append([
            session_id,
            fmt_int(record.get("Requests")),
            fmt_int(record.get("PromptTokensEstimated")),
            fmt_int(record.get("PromptTokensActual")),
            ratio_str(record.get("PromptTokensActual") or 0, record.get("PromptTokensEstimated") or 0),
            fmt_int(record.get("NumToolsTotal")),
            window_str(record.get("FirstRequestAt"), record.get("LastRequestAt")),
        ])
    return rows


CONTEXT_HEADERS = ["session_id", "requests", "est_tokens", "actual_tokens", "ratio a/e", "num_tools", "ventana"]
CONTEXT_ALIGNS = ["l", "r", "r", "r", "r", "r", "l"]


def section_contexts(contexts, session_records, agg_key, agg_record, agg_client_count,
                     top_actual, top_requests, part_type_totals, role_totals, top_n, verbose):
    if not isinstance(contexts, dict) or not contexts:
        return ["no disponible"]
    lines = []
    lines.append("Total de records: {} | por sesión: {} | agregado de cliente: {}".format(
        fmt_int(len(contexts)), fmt_int(len(session_records)),
        fmt_int(len(contexts) - len(session_records))))
    lines.append("")

    if agg_key is not None and agg_record is not None:
        lines.append("Agregado de cliente ({}):".format(agg_key))
        lines.append(kv("requests", fmt_int(agg_record.get("Requests"))))
        lines.append(kv("prompt_tokens_estimated", fmt_int(agg_record.get("PromptTokensEstimated"))))
        lines.append(kv("prompt_tokens_actual", fmt_int(agg_record.get("PromptTokensActual"))))
        lines.append(kv("num_tools_total", fmt_int(agg_record.get("NumToolsTotal"))))
        lines.append(kv("ventana", window_str(
            agg_record.get("FirstRequestAt"), agg_record.get("LastRequestAt"))))
        if agg_client_count and agg_client_count > 1:
            lines.append("  (nota: hay {} clientes, mostrando el de mayor tokens)".format(
                agg_client_count))
    else:
        lines.append("Agregado de cliente: no disponible")
    lines.append("")

    lines.append("Top {} sesiones por prompt_tokens_actual:".format(top_n))
    if top_actual:
        lines.extend(render_table(CONTEXT_HEADERS, context_table_rows(top_actual), aligns=CONTEXT_ALIGNS))
    else:
        lines.append("  (sin datos)")

    lines.append("")
    lines.append("Top {} sesiones por requests:".format(top_n))
    top_actual_ids = [session_id for session_id, _ in top_actual]
    top_requests_ids = [session_id for session_id, _ in top_requests]
    if not top_actual:
        lines.append("  (sin datos)")
    elif top_requests_ids == top_actual_ids:
        lines.append("  idéntico al listado por prompt_tokens_actual")
    else:
        lines.extend(render_table(CONTEXT_HEADERS, context_table_rows(top_requests), aligns=CONTEXT_ALIGNS))
    lines.append("")

    lines.append("Agregado de part_types (todos los records):")
    if part_type_totals:
        total_bytes = sum(bucket["bytes"] for _, bucket in part_type_totals)
        total_est = sum(bucket["est_tokens"] for _, bucket in part_type_totals)
        rows = []
        for part_type, bucket in part_type_totals:
            rows.append([
                part_type,
                fmt_int(bucket["count"]),
                fmt_int(bucket["bytes"]),
                pct(bucket["bytes"], total_bytes),
                fmt_int(bucket["est_tokens"]),
                pct(bucket["est_tokens"], total_est),
            ])
        lines.extend(render_table(
            ["type", "count", "bytes", "%bytes", "est_tokens", "%est"],
            rows,
            aligns=["l", "r", "r", "r", "r", "r"],
        ))
    else:
        lines.append("  (sin datos)")
    lines.append("")

    lines.append("Agregado de roles (todos los records):")
    if role_totals:
        total_est = sum(bucket["est_tokens"] for _, bucket in role_totals)
        rows = []
        for role, bucket in role_totals:
            rows.append([
                role,
                fmt_int(bucket["messages"]),
                fmt_int(bucket["est_tokens"]),
                pct(bucket["est_tokens"], total_est),
            ])
        lines.extend(render_table(
            ["role", "messages", "est_tokens", "%est"],
            rows,
            aligns=["l", "r", "r", "r"],
        ))
    else:
        lines.append("  (sin datos)")

    if verbose:
        lines.append("")
        lines.append("Historial de top 3 sesiones por prompt_tokens_actual (10 más recientes):")
        for rank, (session_id, record) in enumerate(top_actual[:3], 1):
            lines.append("  -- {}. {} --".format(rank, session_id))
            history = sorted(
                record.get("History") or [],
                key=lambda entry: entry.get("RequestAt") or 0,
                reverse=True,
            )[:10]
            if not history:
                lines.append("    (sin history)")
                continue
            rows = []
            for entry in history:
                rows.append([
                    (entry.get("RequestID") or "-")[:12],
                    str(entry.get("Model") or "-"),
                    str(entry.get("NumTools") or 0),
                    fmt_int(entry.get("TotalBytes")),
                    fmt_int(entry.get("PromptTokensEstimated")),
                    "-" if entry.get("PromptTokensActual") is None else fmt_int(entry.get("PromptTokensActual")),
                    ts_to_iso(entry.get("RequestAt")),
                ])
            lines.extend(render_table(
                ["request_id", "model", "num_tools", "total_bytes", "est", "actual", "request_at"],
                rows,
                aligns=["l", "l", "r", "r", "r", "r", "l"],
                indent=4,
            ))
    return lines


def section_sessions(session_records, top_n):
    if not session_records:
        return ["no disponible"]
    lines = []
    lines.append("Top {} sesiones por prompt_tokens:".format(top_n))
    rows = []
    for record in session_records[:top_n]:
        rows.append([
            record["session_id"],
            fmt_int(record["requests"]),
            fmt_int(record["prompt"]),
            fmt_int(record["completion"]),
            fmt_int(record["total"]),
            fmt_float(record["cost"]),
            window_str(record["first"], record["last"]),
        ])
    lines.extend(render_table(
        ["session_id", "requests", "prompt_tokens", "completion_tokens", "total_tokens", "cost_usd", "ventana"],
        rows,
        aligns=["l", "r", "r", "r", "r", "r", "l"],
    ))
    return lines


def section_usage_cost(state, top_n):
    cost_usd = state.get("cost_usd")
    prompt_tokens = state.get("prompt_tokens")
    if not isinstance(cost_usd, dict) or not isinstance(prompt_tokens, dict) \
            or not cost_usd or not prompt_tokens:
        return ["no disponible"]
    total_prompt = sum(value or 0 for value in prompt_tokens.values())
    total_cost = sum(value or 0 for value in cost_usd.values())
    top_cost = sorted(cost_usd.items(), key=lambda item: item[1] or 0, reverse=True)[:top_n]
    top_prompt = sorted(prompt_tokens.items(), key=lambda item: item[1] or 0, reverse=True)[:top_n]
    lines = []
    lines.append("Top {} por cost_usd:".format(top_n))
    lines.extend(render_table(
        ["cliente|provider|model", "cost_usd"],
        [[key, fmt_float(value)] for key, value in top_cost],
        aligns=["l", "r"],
    ))
    lines.append("")
    lines.append("Top {} por prompt_tokens:".format(top_n))
    lines.extend(render_table(
        ["cliente|provider|model", "prompt_tokens"],
        [[key, fmt_int(value)] for key, value in top_prompt],
        aligns=["l", "r"],
    ))
    lines.append("")
    lines.append("Totales: prompt_tokens={} | cost_usd=${}".format(fmt_int(total_prompt), fmt_float(total_cost)))
    return lines


def section_telemetry(telemetry_stats, top_n):
    if telemetry_stats is None:
        return None  # archivo inexistente → omitir la sección
    if "error" in telemetry_stats:
        return ["no disponible: {}".format(telemetry_stats["error"])]
    lines = []
    lines.append("Total de eventos: {}".format(fmt_int(telemetry_stats["events"])))
    if telemetry_stats["parse_errors"]:
        lines.append("Líneas no parseables (ignoradas): {}".format(fmt_int(telemetry_stats["parse_errors"])))
    lines.append("Rango de timestamps: {} → {}".format(
        normalize_iso(telemetry_stats["first_ts"]), normalize_iso(telemetry_stats["last_ts"])))
    lines.append("")
    if telemetry_stats["user_agents"]:
        lines.append("Eventos por User-Agent:")
        lines.extend(render_table(
            ["user_agent", "eventos"],
            [[user_agent, fmt_int(count)] for user_agent, count in telemetry_stats["user_agents"].most_common()],
            aligns=["l", "r"],
        ))
        lines.append("")
    lines.append("Sesiones: {} distintas | {} eventos con sesión | {} sin sesión".format(
        fmt_int(len(telemetry_stats["session_counter"])),
        fmt_int(telemetry_stats["with_session"]),
        fmt_int(telemetry_stats["without_session"])))
    lines.append("Top {} sesiones por eventos:".format(top_n))
    top_sessions = telemetry_stats["session_counter"].most_common(top_n)
    if top_sessions:
        lines.extend(render_table(
            ["session_id", "eventos"],
            [[session_id, fmt_int(count)] for session_id, count in top_sessions],
            aligns=["l", "r"],
        ))
    else:
        lines.append("  (sin datos)")
    return lines


def build_synthesis(top_actual, part_type_totals, cost_usd, session_records):
    lines = []
    dominant_type = part_type_totals[0][0] if part_type_totals else "contenido"
    top_session_id = top_actual[0][0] if top_actual else "—"

    if top_actual:
        session_id, record = top_actual[0]
        lines.append(
            "- La sesión {} concentra {} tokens de prompt actuales en {} requests "
            "(estimado {}, ratio {}).".format(
                session_id,
                fmt_int(record.get("PromptTokensActual") or 0),
                fmt_int(record.get("Requests") or 0),
                fmt_int(record.get("PromptTokensEstimated") or 0),
                ratio_str(record.get("PromptTokensActual") or 0, record.get("PromptTokensEstimated") or 0),
            )
        )
    if part_type_totals:
        part_type, bucket = part_type_totals[0]
        lines.append(
            "- El part_type '{}' domina el payload con {} bytes y {} tokens estimados.".format(
                part_type, fmt_int(bucket["bytes"]), fmt_int(bucket["est_tokens"])))
    if isinstance(cost_usd, dict) and cost_usd:
        total_cost = sum(value or 0 for value in cost_usd.values())
        top_key, top_value = max(cost_usd.items(), key=lambda item: item[1] or 0)
        lines.append(
            "- El modelo {} acumula ${} del costo total ${} ({}).".format(
                top_key, fmt_float(top_value), fmt_float(total_cost), pct(top_value, total_cost)))
    if session_records:
        top_session = session_records[0]
        lines.append(
            "- En 'sessions', {} lidera con {} prompt tokens.".format(
                top_session["session_id"], fmt_int(top_session["prompt"])))
    lines.append(
        "- Optimización sugerida: resumir/recortar '{}' en la sesión {} y compactar "
        "contexto para evitar relectura.".format(dominant_type, top_session_id))
    return lines


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def parse_args(argv):
    parser = argparse.ArgumentParser(
        description="Analiza state.json y telemetry.jsonl de mofgw para optimizar contexto/costo.")
    parser.add_argument("--state", default="/home/<user>/.config/mofgw/state.json",
                        help="Ruta al state.json de mofgw (default: ~/.config/mofgw/state.json)")
    parser.add_argument("--telemetry", default="/home/<user>/logs/mofgw-telemetry.jsonl",
                        help="Ruta al telemetry.jsonl (default: ~/logs/mofgw-telemetry.jsonl)")
    parser.add_argument("--top", type=int, default=10,
                        help="Cuántas sesiones listar (default: 10)")
    parser.add_argument("--verbose", action="store_true",
                        help="Imprime detalle de history de las top sesiones")
    return parser.parse_args(argv)


def main(argv=None):
    args = parse_args(argv)
    try:
        state = load_state(args.state)
    except (OSError, json.JSONDecodeError) as exc:
        print("Error leyendo state '{}': {}".format(args.state, exc), file=sys.stderr)
        return 1

    contexts = state.get("contexts")
    if isinstance(contexts, dict) and contexts:
        session_records = context_session_records(contexts)
        agg_key, agg_record, agg_client_count = context_client_aggregate(contexts)
        top_actual = top_context_sessions(session_records, args.top, by_actual_tokens=True)
        top_requests = top_context_sessions(session_records, args.top, by_actual_tokens=False)
        part_type_totals = aggregate_part_types(contexts)
        role_totals = aggregate_roles(contexts)
    else:
        session_records, agg_key, agg_record, agg_client_count = [], None, None, 0
        top_actual, top_requests = [], []
        part_type_totals, role_totals = [], []

    sessions = state.get("sessions")
    sessions_records = build_session_records(sessions) if isinstance(sessions, dict) and sessions else []

    telemetry_stats = load_telemetry(args.telemetry)

    sections = [
        ("Resumen del state", section_summary(state)),
         ("Data de composición (contexts)",
          section_contexts(contexts, session_records, agg_key, agg_record, agg_client_count,
                           top_actual, top_requests, part_type_totals, role_totals, args.top, args.verbose)),
        ("Consumo por sesión (sessions)", section_sessions(sessions_records, args.top)),
        ("Costo por provider/modelo (usage/cost)", section_usage_cost(state, args.top)),
    ]
    telemetry_section = section_telemetry(telemetry_stats, args.top)
    if telemetry_section is not None:
        sections.append(("Telemetría", telemetry_section))
    sections.append(("Síntesis",
                     build_synthesis(top_actual, part_type_totals, state.get("cost_usd"), sessions_records)))

    for title, lines in sections:
        print()
        print("## " + title)
        for line in lines:
            print(line)
    return 0


if __name__ == "__main__":
    sys.exit(main())
