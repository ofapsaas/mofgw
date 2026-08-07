# projectbrief.md — mofgw (MOF Gateway)

> Memory Bank del proyecto mofgw. Documento raíz: qué es, por qué existe, decisiones irreversibles.

## Identidad

- **Nombre:** mofgw (MOF Gateway)
- **Qué es:** Proxy de IA self-hosted en Go. Expone UN endpoint OpenAI-compatible y hace fallback transparente entre providers.
- **Go module:** `github.com/ofapsaas/mofgw`
- **Puerto:** 3369 (default)
- **Licencia:** GPL-3.0-or-later
- **Copyright:** © 2026 Pablo Manuel Rizzo

## Por qué existe

1. **omniroute** (Node, ~1GB RAM, 55s arranque) no anda bien y es pesadísimo.
2. **Fallback nativo de OpenClaw** no sirve para crons/hb/sesiones aisladas.
3. **Salida progresiva de Node** — kernel-ia será el SO de agentes; mofgw es un ladrillo de esa arquitectura.
4. **Ligero, controlado, mantenible** — ~10-12MB RAM, binario Go estático.

## Principio rector

**El cliente nunca se entera.** El proxy salva todas las situaciones: fallback, retry, cooldown, absorción de errores. El agente cliente (OpenClaw, crons, sesiones aisladas) ve UN endpoint que siempre responde.

## Decisiones irreversibles

| Decisión | Valor | Quién | Cuándo |
|----------|-------|-------|--------|
| Nombre | mofgw | Pablo | 03 Ago 2026 |
| Transparencia | Total — proxy transparente puro, no interpreta tools/parámetros | Pablo | 03 Ago |
| Deploy | systemd user, básico, mínimo | Pablo | 03 Ago |
| Puerto | 3369 (menos usado del rango 1025-4000, sin asignación IANA) | Pablo | 03 Ago |
| Config | `~/.config/mofgw/` (home) o `/etc/mofgw/` (sistema) | Pablo | 03 Ago |
| Auth | API key por cliente (Bearer), in-scope del MVP | Pablo | 04 Ago |
| Tools | NO strip — proxy transparente puro (limitación documentada) | Pablo | 05 Ago |
| Eficiencia | Cache providers + medición + catálogo + contexto | Pablo | 06 Ago |
| Delegación replanteo | Total a Ofap (auto-aprobación GVR) | Pablo | 06 Ago |

## Arquitectura (una línea)

```
cliente (OpenClaw/cron/sesión) → mofgw:3369 → providers upstream en orden de config
                                                (acct1 → acct2 → qwen → zen free)
```

Internamente: `httputil.ReverseProxy` + interfaz Provider + mapa de cooldown en RAM + reescritura de `model` + config YAML.

## Epics del programa (8 epics)

1. **EPIC-001 mofgw-core** — MVP: endpoint único, fallback en cadena circular, clamp, streaming, timeouts, config, auth → ✅ DONE
2. **EPIC-002 mofgw-resiliencia** — Transparencia total: retry/backoff, health checks, absorción, degradación → ✅ DONE
3. **EPIC-003 mofgw-eficiencia** — Cache de providers: instrumentación cache hit/miss → ✅ DONE
4. **EPIC-004 mofgw-escala** — Alta carga multi-agente: límites concurrencia + aislamiento → ✅ DONE
5. **EPIC-005 mofgw-ops** — Metrics Prometheus, logging JSON, systemd, config paths, verbose → ✅ DONE
6. **EPIC-006 mofgw-observabilidad** — Medición tokens/costos por cliente/provider/modelo → ✅ DONE
7. **EPIC-007 mofgw-catalogo** — /v1/models enriquecido (capabilities, pricing) + headers X-Usage-* → ✅ DONE
8. **EPIC-008 mofgw-contexto** — Rechazo temprano por ventana, budget por cliente, stats por sesión → ✅ DONE

Más: **SEC-001-security-hardening** (auditoría externa) → ✅ DONE

## Stakeholders

- **Pablo** — autor del proyecto, aprobador del plan y specs
- **Ofap** — operador/agente que implementó (auto-aprobación GVR delegada por Pablo)
- **Agentes futuros** — consumidores del proxy (OpenClaw, crons, sesiones aisladas, heartbeat)
