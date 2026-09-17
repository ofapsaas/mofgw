# E3 — Integración cross-feature del epic 019-provider-sync-automation

**Epic:** 019-provider-sync-automation · **Etapa:** E3 (Integración) · **Fecha:** 2026-09-17
**Estado del loop:** 7/7 features done y mergeadas. Suite Go: **974/37 `-race`** (post-fix E3), harness `test-install.sh`: **48/48**, `go vet` limpio, `go build` OK.

## 1. Tests E2E cross-feature (en suite, deterministas)

| Test | Qué integra | Evidencia |
|---|---|---|
| `TestRun_OnceCycle` (B18, 004) | config + caches seedeadas + `catalogmerge` + `configsync` + binario: ciclo completo con fixtures reales, sidecar, segunda corrida → skip con mtime intacto | suite verde |
| `TestRun_Parity` + `TestRun_ExpectedModelSet` (B7/B16, 005) | config escrito → `ParseForValidation` → `/v1/models` fake: set esperado = unión dedup de providers (incl. subprocess), orden irrelevante, `created` jamás participa | suite verde |
| `TestRun_NoFetchCacheOnly` (B17, 004) + `TestRun_OnceCycle` | todas-nil → exit 1 sin escribir; degradación por fuente | suite verde |
| `TestSerialize_ValidatedCandidate` (B10, 004) | candidato → `ParseForValidation` 0-error, resto idéntico | suite verde |
| `TestRun_RollbackRestoreOnly/Exhausted` (B9/B11, 005) | fallo de verificación → restore de bytes previos + re-restart + exit 3 | suite verde |
| `TestRun_ExitCodesReload` (B2, 005, matriz 0/1/2/3) | combinaciones 004×reload sobre fixtures reales | suite verde |
| Canaries opt-in | `TestRoundTrip_LiveConfig` (P16, 004), `TestReloadSig_LiveEnvironment` (C14, 005), `TestUnits_LiveEnvironment` (C10, 006), `TestSnapshot_LiveEmbeddedParses` (C11, 007) — todos skip sin sus guardias | skip correcto en CI |

## 2. Verificación E2E contra upstreams REALES (sandbox, evidencia empírica)

**Setup (cero impacto en producción):** binarios construidos del HEAD; config = copia del config vivo en `/tmp`; caches aisladas (`XDG_CACHE_HOME` propio); env del operador cargado (las keys las exige `resolveKeys`, igual que el server); `--no-reload` (jamás toca el mofgw.service real, que sigue activo en el host).

**Corrida 1 (16:52, pre-fix E3):** fetch real OK — models.dev 4.692.490 bytes, zen 5.976, go 3.144, openrouter 736.662. Merge determinístico + warnings del IR. **FALLÓ en validación pre-commit (fail-loud correcto):**
`model_metadata "qwen3.8-flash": thinking_default "high" no está en thinking [low medium xhigh]`
→ mejora incorporada de la convención de tests del framework: models.dev trajo niveles nuevos (`xhigh`) + default manual viejo ⇒ la regla P4c de 004 (preservar siempre) producía un candidato inválido en CADA corrida (fail-loud perpetuo). **El fail-loud funcionó; la regla estaba incompleta.**

**Fix contract-primero (commit `5803a85`):** enmienda P4c del spec de 004 (consistencia: default fuera de los niveles derivados ⇒ se omite + warning) + rework de B6 (caso compatible + caso stale) + implementación en `mergeKeyedSection` (warnings al reporte). Suite 974/37 verde.

**Corrida 2 (16:55, post-fix):** `ciclo completo applied=true skipped=false digest=b5d7c4… sources="[go modelsdev openrouter zen]"`. **Verificado en el artefacto:** `model_metadata qwen/qwen3.8-flash` con `thinking: [low, medium, xhigh]` (niveles reales nuevos), `thinking_default` OMITIDO, comentarios del operador preservados byte a byte, pricing/metadata re-derivados, sidecar creado. Segunda corrida → skip (digest match, mtime intacto).

## 3. Criterios de aceptación del epic (plan.md:59-62) — estado

| Criterio | Estado | Evidencia |
|---|---|---|
| `mofgw-sync --once` ejecuta el ciclo completo contra upstreams reales | ✅ VERIFICADO | corrida 2 en sandbox (§2): fetch real 4 fuentes + merge + validación + write atómico |
| `GET /v1/models` local refleja paridad de IDs con las fuentes autorizadas | ⏳ PENDIENTE DE DEPLOY | la paridad está congelada por tests (B7/B16) y el candidato verificado pre-write; la aplicación real requiere restart del server (005) = acción de deploy del operador (ver §4) |
| Timer systemd activo con logs del sync | ⏳ PENDIENTE DE DEPLOY | units commiteados + install.sh verificado en sandbox (harness 48/48); la activación real (`install.sh` + `enable --now`) es deploy (ver §4) |
| Suite Go completa verde (`-race`), sin regresiones | ✅ VERIFICADO | 974/37 + vet limpio + harness 48/48 (+ canaries skip) |

## 4. Activación en producción (único paso pendiente — acción del operador)

El loop de código está completo y verificado; lo que resta es **deploy**, no desarrollo:

```bash
cd ~/clawd/projects/mofgw   # o el checkout de deploy
./scripts/install.sh        # instala mofgw-sync + units (con backup-on-overwrite) + enable --now del timer
systemctl --user list-timers mofgw-sync.timer   # verificar agenda
```

Efectos esperados del deploy: primera corrida inmediata (OnBootSec vencido — DESEADO, deja logs visibles); si el catálogo cambió → write + restart del server con verificación (005) o rollback si falla; si no cambió → skip silencioso. Monitorear: `journalctl --user -u mofgw-sync` (fases del ciclo), warnings de staleness del snapshot (> 30d → `scripts/fetch-snapshot.sh`), `MOFGW_SYNC_VERIFY_KEY` en `~/.config/mofgw/env` para paridad automatizada (sin ella: degradación F1+F2 con warning).

**Riesgos operativos conocidos y aceptados:** restart corta streams en curso (drain 10s; tolerancia de negocio pendiente de definir — R2 de 005); M-2 skip-trap produce fail-loud por corrida hasta intervención (comportamiento deseado, log m2_check con ambos digests); flakes preexistentes `TestE2E010002_TTLExpiry` + `TestPostcondition9_MuestreoBanda` (estadísticos, fuera del epic).

## 5. Contratos cross-feature verificados

- 003→004: IR `catalogmerge.Plan` consumido por `configsync.Serialize` (asociación 1:1, orden vivo, merge-back) — congelado por B1-B11 de 004 + enmienda P4c E3.
- 004→005: reporte `Applied/Skipped/Digest` + sidecar; defensa M-2 del lado 005 (sin tocar 004) — congelado por B3/B4 de 005.
- 004 es el único escritor de config.yaml (+ excepción rollback restore-only de 005, I3) — verificado por superficie de diffs de cada feature.
- 001/002→007: `modelsdev.ParseCatalog` como parser del snapshot (P2) — congelado por B4 de 007.
- 006 agenda `mofgw-sync --once` (default) — order P6 (timer jamás sobre install fallido) congelado por harness C7.
