# systemPatterns.md — Patrones técnicos de mofgw

> Memory Bank: convenciones y patrones recurrentes. Creado 2026-09-11 (deuda de
> bootstrap desde epic 010); los patrones previos del repo se capturan en ADRs y
> specs — este archivo se alimenta hacia atrás al retomar épicas pausadas.

## Cache persistente en disco con lock + digest (patrón modelsdev, ADR-011)

Primer uso en el repo de `os.UserCacheDir` y `syscall.Flock` (019-001-fetch-modelsdev).
Reglas: lock sobre archivo dedicado `<path>.lock` (nunca el propio artefacto — el rename
atómico invalidaría locks sobre él y bloquearía lectores); digest sha256 en sidecar
`<path>.sha256` para skip de writes byte-idénticos; escritura SIEMPRE temp+rename
(patrón `internal/metrics/persist.go`), sidecar primero / dato último = punto de commit;
TTL por mtime; fail-soft si existe cache / fail-loud si no; errores tipados `errors.Is`.
Reutilizable por futuros fetchers de catálogos upstream (019-002/003/007).

## Convenciones generales del repo (referencia cruzada)

- API keys NUNCA en YAML: solo `api_key_env` refs (config.go).
- Knobs declarativos por provider (precedente `thinking_path`, `opencode_session`):
  jamás inferidos del ID/base_url/modelo.
- Fail-soft para datos re-generables (state corrupto no bloquea arranque);
  fail-fast para config inválida al cargar.
- Defaults en constructores de paquete (`<= 0 → default`), compatibilidad aditiva
  off-safe (knobs default-off, configs existentes cargan idéntico).
- Errores tipados comparables con `errors.Is` + wrap `%w` (patrón `ErrUpstream`).
- Escritura atómica temp+rename SIEMPRE (patrón persist.go).
