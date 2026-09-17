// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizza
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-004-atomic-write-validate — binario
// cmd/mofgw-sync. Cubren C14 y C16 (P12, P14, P15).
//
// Contratos a materializar por GREEN (definidos por el test-writer,
// precedente buildTelemetryFromConfig de cmd/mofgw — el implementer DEBE
// respetar estas firmas):
//
//	func run(opts runOpts, logger *slog.Logger) int
//	// logger nil → slog.Default(). Retorna el EXIT CODE (P15): 0/1/2.
//	// main() = os.Exit(run(parseArgs(...))) — el mapeo parse-error→2 es de main.
//
//	func parseArgs(args []string) (runOpts, error)
//	// parsea -config/--no-fetch (agregación documentada del test-audit §4-E:
//	// la firma run(opts,…) no ve los argv crudos; parseArgs cubre exit 2).
//
//	type runOpts struct {
//		ConfigPath string    // -config (precedencia idéntica a config.Load)
//		NoFetch    bool      // --no-fetch: stores cache-only, sin red (P14)
//		CachePaths cachePaths // inyección de paths de cache por fuente (tests)
//	}
//	type cachePaths struct {
//		ModelsDev, Zen, Go, OpenRouter string
//	}
//
// RED por compilación: el paquete cmd/mofgw-sync no existe (precedente
// 001/002/003).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/upstream"
)

// ---- fixtures (composición mínima, test-audit §6.3) ----

// syncConfigTemplate: config del binario con UN provider zen declarando
// models en orden NO alfabético (para que el candidato cambie de verdad) y
// SIN pricing/metadata (el ciclo agrega entries P4d).
const syncConfigTemplate = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
providers:
  - id: zen-acc
    base_url: "%s"
    api_key_env: "MOFGW_ZEN_KEY"
    models: ["minimax-m3", "glm-5.2"]
    max_tokens: 8192
`

// syncConfigFantasma: igual + un id declarado ausente en models.dev →
// warning garantizado del IR (003, lockeado en catalogmerge.go:126, I3):
// "<providerID>/<modelID>: no encontrado en models.dev — sin pricing/metadata"
// → "zen-acc/id-fantasma: no encontrado en models.dev — sin pricing/metadata".
const syncConfigFantasma = `
server:
  addr: "127.0.0.1:3369"
fallback:
  max_retries: 2
  cooldown: 60s
  timeout: 120s
providers:
  - id: zen-acc
    base_url: "%s"
    api_key_env: "MOFGW_ZEN_KEY"
    models: ["minimax-m3", "glm-5.2", "id-fantasma"]
    max_tokens: 8192
`

// mdBody: recorte del catálogo models.dev (shape real de la API, fuente
// catalogmerge_test.go mdShared): espejo default del source zen = "opencode",
// con cost para que derive pricing de glm-5.2/minimax-m3 pero NADA para
// id-fantasma.
const mdBody = `{
  "opencode": {
    "name": "OpenCode",
    "models": {
      "glm-5.2": {
        "name": "GLM 5.2",
        "reasoning": true,
        "tool_call": true,
        "limit": {"context": 200000, "output": 128000},
        "cost": {"input": 1.4, "output": 4.4, "cache_read": 0.26}
      },
      "minimax-m3": {
        "name": "MiniMax M3",
        "reasoning": true,
        "tool_call": true,
        "limit": {"context": 1000000, "output": 16000},
        "cost": {"input": 0.3, "output": 1.2, "cache_read": 0.03}
      }
    }
  }
}`

// zenBody: lista de acceso OpenAI-shape (tolerante, upstream_test.go
// TestParseModelList_Tolerant): contiene los ids declarados reales.
const zenBody = `{
  "object": "list",
  "data": [
    {"id": "minimax-m3", "object": "model", "owned_by": "opencode", "created": 1},
    {"id": "glm-5.2", "object": "model", "owned_by": "opencode", "created": 1}
  ]
}`

// ---- helpers ----

func captureLogs() (*bytes.Buffer, *slog.Logger) {
	var buf bytes.Buffer
	return &buf, slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func captureLoggerOnly(t *testing.T) *slog.Logger {
	t.Helper()
	_, l := captureLogs()
	return l
}

func fillZenURL(template string) string {
	return strings.Replace(template, "%s", upstream.ZenURL, 1)
}

// writeSyncConfig escribe el config del binario y devuelve su path.
func writeSyncConfig(t *testing.T, dir, template string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(fillZenURL(template)), 0o600); err != nil {
		t.Fatalf("escribiendo config del sync: %v", err)
	}
	return path
}

// writeCacheFile + writeSidecar: réplica exacta del patrón de
// upstream_test.go/modelsdev_test.go (body + <path>.sha256, 0o600).
func writeCacheFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writeCacheFile: %v", err)
	}
}

func writeSidecar(t *testing.T, path string, body []byte) {
	t.Helper()
	sum := sha256.Sum256(body)
	if err := os.WriteFile(path+".sha256", []byte(hex.EncodeToString(sum[:])), 0o600); err != nil {
		t.Fatalf("writeSidecar: %v", err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leyendo %s: %v", path, err)
	}
	return b
}

func seedZenCache(t *testing.T, zenPath string) {
	t.Helper()
	writeCacheFile(t, zenPath, []byte(zenBody))
	writeSidecar(t, zenPath, []byte(zenBody))
}

func seedModelsDevCache(t *testing.T, mdPath string) {
	t.Helper()
	writeCacheFile(t, mdPath, []byte(mdBody))
	writeSidecar(t, mdPath, []byte(mdBody))
}

func envKeySet(t *testing.T) {
	t.Helper()
	t.Setenv("MOFGW_ZEN_KEY", "k1")
}

// ---- B16 (C14, P12) — warnings passthrough + logueados ANTES de escribir ----

// TestRun_LogsWarnings congela P12: todos los warnings del Plan (+ per-provider)
// se loguean (slog, uno por warning) ANTES de escribir; no afectan el exit
// code (fail-soft de 003). El id declarado "id-fantasma" ausente en models.dev
// produce el warning del IR "zen-acc/id-fantasma: no encontrado en
// models.dev — sin pricing/metadata" (texto lockeado, catalogmerge.go:126);
// el aserto usa el sufijo estable "no encontrado en models.dev".
func TestRun_LogsWarnings(t *testing.T) {
	envKeySet(t)

	// warnIdAusenteSuffix: fragmento estable del warning real del IR —
	// describe el par provider/modelo ausente y el efecto (sin pricing).
	const warnIdAusenteSuffix = "no encontrado en models.dev"

	t.Run("loguea_una_vez_por_warning_y_exit_0", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeSyncConfig(t, dir, syncConfigFantasma)
		mdPath := filepath.Join(dir, "cache", "modelsdev.json")
		zenPath := filepath.Join(dir, "cache", "zen.json")
		seedModelsDevCache(t, mdPath)
		seedZenCache(t, zenPath)

		buf, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
		}, logger)
		if code != 0 {
			t.Fatalf("exit code con warnings = %d, want 0 (P12: fail-soft, salvo error de Merge)", code)
		}
		if got := strings.Count(buf.String(), warnIdAusenteSuffix); got != 1 {
			t.Errorf("warning %q logueado %d veces, want exactamente 1 (P12: uno por warning)", warnIdAusenteSuffix, got)
		}
	})

	t.Run("loguea_antes_de_escribir_caso_write_abortado", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("como root el chmod read-only no bloquea — caso no determinístico")
		}
		dir := t.TempDir()
		configPath := writeSyncConfig(t, dir, syncConfigFantasma)
		mdPath := filepath.Join(dir, "cache", "modelsdev.json")
		zenPath := filepath.Join(dir, "cache", "zen.json")
		seedModelsDevCache(t, mdPath)
		seedZenCache(t, zenPath)

		// sidecar ausente + dir read-only → el write falla (I/O → exit 1);
		// los warnings YA deben estar en el buffer (P12: ANTES de escribir).
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatalf("chmod read-only: %v", err)
		}
		buf, logger := captureLogs()
		code := run(runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: mdPath, Zen: zenPath},
		}, logger)
		if code != 1 {
			t.Fatalf("exit code con write fallido = %d, want 1 (P15: I/O)", code)
		}
		if !strings.Contains(buf.String(), warnIdAusenteSuffix) {
			t.Error("warnings NO logueados a pesar del abort de escritura (P12: logueados ANTES de escribir)")
		}
	})
}

// ---- B17 (C16, P14) — --no-fetch cache-only ----

// TestRun_NoFetchCacheOnly congela P14: cada fuente se lee de su Store.Get()
// (sin red); cache ausente → fuente nil → fail-soft (degradación + warning);
// TODAS nil → error de Merge → exit 1 sin escribir.
func TestRun_NoFetchCacheOnly(t *testing.T) {
	envKeySet(t)

	t.Run("todas_nil_exit_1_sin_escribir", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeSyncConfig(t, dir, syncConfigTemplate)
		before := readBytes(t, configPath)
		_, logger := captureLogs()
		opts := runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			CachePaths: cachePaths{ModelsDev: filepath.Join(dir, "cache", "inexistente-md"), Zen: filepath.Join(dir, "cache", "inexistente-zen")},
		}
		code := run(opts, logger)
		if code != 1 {
			t.Fatalf("exit code sin fuentes = %d, want 1 (P14: error de Merge 003 P12d)", code)
		}
		if got := readBytes(t, configPath); string(got) != string(before) {
			t.Error("config modificado tras exit 1 (P15: en CUALQUIER exit 1 config byte-intacto)")
		}
	})

	t.Run("cache_zen_presente_exit_0_sin_red", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeSyncConfig(t, dir, syncConfigTemplate)
		before := readBytes(t, configPath)
		zenPath := filepath.Join(dir, "cache", "zen.json")
		seedZenCache(t, zenPath)

		buf, logger := captureLogs()
		opts := runOpts{
			ConfigPath: configPath,
			NoFetch:    true,
			// modelsdev ausente → fail-soft de degradación + warning (P14).
			CachePaths: cachePaths{Zen: zenPath, ModelsDev: filepath.Join(dir, "cache", "inexistente-md")},
		}
		code := run(opts, logger)
		if code != 0 {
			t.Fatalf("exit code con cache zen = %d, want 0 (P14: Get cache-only)", code)
		}
		after := readBytes(t, configPath)
		if string(after) == string(before) {
			t.Error("candidato == config vigente: el plan no aplicó el sort de models (P2/D9)")
		}
		if !strings.Contains(string(after), `models: ["glm-5.2", "minimax-m3"]`) {
			t.Errorf("models no quedaron sorted (P11 de 003):\n%s", after)
		}
		if _, err := os.Stat(configPath + ".sha256"); err != nil {
			t.Errorf("sidecar no creado tras el write: %v", err)
		}
		if !strings.Contains(buf.String(), "level=WARN") {
			t.Errorf("se esperaba fail-soft (warning) por la fuente modelsdev ausente (P14); log: %s", buf.String())
		}
	})
}

// ---- B18 (C16, P15) — ciclo --once completo + skip ----

// TestRun_OnceCycle congela P15: load raw → Parse (fail-loud si el config
// VIGENTE es inválido) → fetch/merge → Serialize → ParseForValidation →
// skip/write → reporte final. Primer run: exit 0, config reescrito (models
// sorted), sidecar escrito. Segundo run: skip byte-idéntico (exit 0, mtime
// intacto). E2E del flujo cross-componente con fixtures reales (sin mocks).
// El assert del contenido es por SUBSTRING (models sorted): la normalización
// cosmética HITL-aceptada (D2) no debe romper el E2E.
func TestRun_OnceCycle(t *testing.T) {
	envKeySet(t)
	dir := t.TempDir()
	configPath := writeSyncConfig(t, dir, syncConfigTemplate)
	before := readBytes(t, configPath)
	zenPath := filepath.Join(dir, "cache", "zen.json")
	seedZenCache(t, zenPath)

	opts := runOpts{
		ConfigPath: configPath,
		NoFetch:    true,
		CachePaths: cachePaths{Zen: zenPath},
	}

	code := run(opts, captureLoggerOnly(t))
	if code != 0 {
		t.Fatalf("primer --once: exit %d, want 0 (P15)", code)
	}
	after := readBytes(t, configPath)
	if bytes.Equal(after, before) {
		t.Fatal("config tras --once == config vigente: el plan no aplicó (P2)")
	}
	if !strings.Contains(string(after), `models: ["glm-5.2", "minimax-m3"]`) {
		t.Errorf("models no quedaron sorted en el candidato:\n%s", after)
	}
	if _, err := os.Stat(configPath + ".sha256"); err != nil {
		t.Errorf("sidecar no creado: %v", err)
	}

	// Segunda corrida: skip byte-idéntico → mtime intacto (exit 0).
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(configPath, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	code = run(opts, captureLoggerOnly(t))
	if code != 0 {
		t.Fatalf("segundo --once (skip): exit %d, want 0 (P15: skipped también es éxito)", code)
	}
	stat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat post-skip: %v", err)
	}
	if !stat.ModTime().Equal(past) {
		t.Errorf("segunda corrida reescribió el config (P10: skip byte-idéntico deja mtime intacto)")
	}
}

// ---- B19 (C16, P15) — exit codes 0/1/2 ----

// TestRun_ExitCodes congela P15: 0 = escrito OK o skipped; 1 = error
// fail-loud (config vigente inválido, path inexistente/I/O, validación del
// candidato falla, Merge sin fuentes); 2 = flags/uso inválido (parse de
// flags; main() mapea el error de parseArgs a exit 2). En CUALQUIER exit 1:
// config.yaml byte-intacto.
func TestRun_ExitCodes(t *testing.T) {
	envKeySet(t)
	dir := t.TempDir()
	configPath := writeSyncConfig(t, dir, syncConfigTemplate)
	zenPath := filepath.Join(dir, "cache", "zen.json")
	seedZenCache(t, zenPath)

	t.Run("exit0_aplicado", func(t *testing.T) {
		opts := runOpts{ConfigPath: configPath, NoFetch: true, CachePaths: cachePaths{Zen: zenPath}}
		if code := run(opts, captureLoggerOnly(t)); code != 0 {
			t.Fatalf("exit = %d, want 0 (aplicado)", code)
		}
	})

	t.Run("exit1_config_vigente_invalido", func(t *testing.T) {
		brokenPath := filepath.Join(dir, "broken.yaml")
		broken := []byte("server: [broken")
		if err := os.WriteFile(brokenPath, broken, 0o600); err != nil {
			t.Fatalf("write broken: %v", err)
		}
		opts := runOpts{ConfigPath: brokenPath, NoFetch: true}
		if code := run(opts, captureLoggerOnly(t)); code != 1 {
			t.Fatalf("exit = %d, want 1 (config vigente inválido, fail-loud)", code)
		}
		if got := readBytes(t, brokenPath); string(got) != string(broken) {
			t.Error("config inválido fue modificado (P15: en CUALQUIER exit 1 config byte-intacto)")
		}
	})

	t.Run("exit1_config_path_inexistente", func(t *testing.T) {
		opts := runOpts{ConfigPath: filepath.Join(dir, "no-existe.yaml"), NoFetch: true}
		if code := run(opts, captureLoggerOnly(t)); code != 1 {
			t.Fatalf("exit = %d, want 1 (I/O: config ausente es fail-loud)", code)
		}
	})

	t.Run("once_aceptado_y_redundante", func(t *testing.T) {
		// Corrección del review B-1 (etapa 4): el flag `--once` de D1.3 no
		// estaba congelado por ningún test (hueco de mapeo del audit:
		// C16→B17/B18/B19, ninguno pasaba --once). Congelado acá PRIMERO
		// (mini-RED); el implementer agrega el flag después.
		//
		// D1.3/P15: --once = ciclo completo, ÚNICO modo de 004 (el
		// timer/scheduling es 006) → el flag es REDUNDANTE con el default:
		// aceptado y NO cambia el comportamiento. Jamás exit 2. Congelamos
		// la aceptación del parseo (`-once` y `--once`, paridad con
		// `-config`; Go flag acepta ambos) — si el flag materializa o no
		// como campo de runOpts es detalle del impl (D1.3: absorbido como
		// no-op). El ciclo completo ya lo congela B18; el ciclo con flag
		// explícito es equivalente por D1.3.
		//
		// Contrato parseArgs (establecido por este suite): recibe los argv
		// de flags SIN el argv[0] del binario (flag.Parse(os.Args[1:])).
		for _, args := range [][]string{
			{"--once"},
			{"-once"},
			{"--once", "-config", configPath},
		} {
			opts, err := parseArgs(args)
			if err != nil {
				t.Fatalf("parseArgs(%v) debería aceptar --once (D1.3/P15: redundante con el default, NO es uso inválido); got: %v", args, err)
			}
			if args[len(args)-1] == "-config" || len(args) == 3 {
				if opts.ConfigPath != configPath {
					t.Errorf("parseArgs(%v) perdió el -config: ConfigPath = %q, want %q", args, opts.ConfigPath, configPath)
				}
			}
		}
	})

	t.Run("exit2_flag_desconocido", func(t *testing.T) {
		// El mapeo a exit 2 vive en main(): os.Exit(2) cuando parseArgs
		// falla. El contrato del test-writer congela que parseArgs rechaza
		// el flag desconocido con error (P15: flags/uso inválido → 2).
		_, err := parseArgs([]string{"-config", configPath, "--bogus"})
		if err == nil {
			t.Fatal("flag desconocido debería fallar el parse (→ exit 2 en main, P15)")
		}
		if !strings.Contains(err.Error(), "bogus") {
			t.Errorf("error debería nombrar el flag desconocido, got: %v", err)
		}
	})

	t.Run("parseargs_flags_validos", func(t *testing.T) {
		opts, err := parseArgs([]string{"-config", configPath, "--no-fetch"})
		if err != nil {
			t.Fatalf("parseArgs de flags válidos: %v", err)
		}
		if opts.ConfigPath != configPath {
			t.Errorf("ConfigPath = %q, want %q", opts.ConfigPath, configPath)
		}
		if opts.NoFetch != true {
			t.Error("NoFetch = false, want true (--no-fetch parseado)")
		}
	})
}

// ---- 019-007-build-snapshot (B3-B8) — fallback offline por snapshot ----
//
// Los tests de esta sección congelan P1/P2/P5-P9/P11: el fallback solo ante
// AUSENCIA de cache (corruption ≠ ausencia), parse con el parser de 001,
// disco byte-intacto, warnings sintéticos, staleness, y comportamiento
// pre-007 con Available=false. Corren con `runSnapshot` INYECTADO (tiny
// sintético) — jamás con el api.json real ni con red (I4).

// snapshotTiny: catálogo sintético mínimo (shape real de api.json, fuente
// catalogmerge_test.go mdShared): espejo "opencode" con cost para derivar
// pricing de glm-5.2, y minimax-m3 SIN limit/cost + campo desconocido
// (tolerancia P14 de 001, B4).
const snapshotTiny = `{
  "opencode": {
    "name": "OpenCode",
    "models": {
      "glm-5.2": {
        "name": "GLM 5.2",
        "limit": {"context": 200000, "output": 128000},
        "cost": {"input": 1.4, "output": 4.4}
      },
      "minimax-m3": {
        "name": "MiniMax M3",
        "campo_desconocido": 42
      }
    }
  }
}`

// snapshotOpts: runOpts con fallback inyectado hacia un cache modelsdev
// INEXISTENTE (ausencia, D4) + Zen/Go/OpenRouter también ausentes (P10).
func snapshotOpts(mdPath string) runOpts {
	return runOpts{
		NoFetch:    true,
		CachePaths: cachePaths{ModelsDev: mdPath, Zen: mdPath + ".zen", Go: mdPath + ".go", OpenRouter: mdPath + ".or"},
		Snapshot: runSnapshot{
			Raw:       []byte(snapshotTiny),
			FetchedAt: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
			SHA256:    "tiny-sha-para-tests",
			Available: true,
		},
	}
}

// ---- B3 (C1, P1+P6) — fallback por ausencia sirve el tiny, disco intacto ----

// TestLoadSources_SnapshotFallback congela P1: Get ausente + snapshot
// disponible → catalog parseado del tiny (providers/models por clave); las
// otras fuentes nil (P10). Y P6: el directorio de cache queda byte-intacto
// (el snapshot se sirve en memoria, jamás se persiste).
func TestLoadSources_SnapshotFallback(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "cache", "modelsdev.json")

	before := readdirNames(t, dir)
	catalog, zenList, goList, orCatalog := loadSources(snapshotOpts(mdPath))

	if catalog == nil {
		t.Fatal("catalog nil con cache ausente + snapshot disponible (P1: fallback por ausencia)")
	}
	op, ok := catalog.Providers["opencode"]
	if !ok {
		t.Fatalf("provider opencode ausente del fallback (P1)")
	}
	if got := op.Models["glm-5.2"].Limit.Context; got != 200000 {
		t.Errorf("opencode/glm-5.2 context = %v, want 200000 (P1: providers/models por clave)", got)
	}
	if _, ok := op.Models["minimax-m3"]; !ok {
		t.Errorf("opencode/minimax-m3 ausente del fallback (P1)")
	}
	if zenList != nil || goList != nil || orCatalog != nil {
		t.Errorf("fuentes sin snapshot deben quedar nil (P10): zen=%v go=%v or=%v", zenList != nil, goList != nil, orCatalog != nil)
	}

	after := readdirNames(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("directorio de cache modificado por el fallback (P6: servir ≠ persistir)\nbefore: %v\nafter:  %v", before, after)
	}
	if _, err := os.Stat(mdPath); !os.IsNotExist(err) {
		t.Errorf("el fallback creó %s (P6/I3: jamás se escribe cache del snapshot)", mdPath)
	}
	if _, err := os.Stat(mdPath + ".sha256"); !os.IsNotExist(err) {
		t.Errorf("el fallback creó sidecar (P6/I3)")
	}
}

func readdirNames(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// ---- B4 (C1, P2) — schema variable tolerante ----

// TestLoadSources_SnapshotTolerantShape congela P2: el tiny con schema
// variable (sin limit, sin cost, campo desconocido) parsea sin error con
// zero-values — misma semántica que P14 de 001.
func TestLoadSources_SnapshotTolerantShape(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "cache", "modelsdev.json")

	catalog, _, _, _ := loadSources(snapshotOpts(mdPath))
	if catalog == nil {
		t.Fatal("catalog nil (P1)")
	}
	mm, ok := catalog.Providers["opencode"].Models["minimax-m3"]
	if !ok {
		t.Fatal("minimax-m3 ausente (P2)")
	}
	if mm.Cost.Input != 0 || mm.Cost.Output != 0 {
		t.Errorf("cost de modelo sin cost = %v, want zero-value (P2)", mm.Cost)
	}
}

// ---- B5 (C3, P5) — cache corrupto NO cae a snapshot ----

// TestLoadSources_CorruptCacheNoSnapshotMask congela P5: cache PRESENTE con
// JSON inválido + snapshot disponible → catalog == nil (error de
// corruption, no ausencia). El snapshot NO enmascara corrupción.
func TestLoadSources_CorruptCacheNoSnapshotMask(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "cache", "modelsdev.json")
	writeCacheFile(t, mdPath, []byte("{json roto"))

	opts := snapshotOpts(mdPath)
	catalog, _, _, _ := loadSources(opts)
	if catalog != nil {
		t.Errorf("catalog servido desde snapshot con cache CORRUPTO (P5: corruption ≠ ausencia)")
	}
}

// ---- B6 (C5, P7) — log del evento + warning sintético en el plan ----

// TestSnapshotFallback_WarningAndLog congela P7: fallback servido → evento
// `snapshot_fallback{source,fetched_at,sha256,age_days}` en el log + el
// warning sintético (substring `catálogo snapshot embebido`, binario-added
// post-Merge) en los warnings logueados del run.
func TestSnapshotFallback_WarningAndLog(t *testing.T) {
	envKeySet(t)
	dir := t.TempDir()
	configPath := writeSyncConfig(t, dir, syncConfigTemplate)
	mdPath := filepath.Join(dir, "cache", "modelsdev.json")
	zenPath := filepath.Join(dir, "cache", "zen.json")
	seedZenCache(t, zenPath)

	opts := snapshotOpts(mdPath)
	opts.ConfigPath = configPath
	opts.CachePaths.Zen = zenPath
	buf, logger := captureLogs()
	code := run(opts, logger)
	if code != 0 {
		t.Fatalf("run con fallback = %d, want 0", code)
	}
	if !strings.Contains(buf.String(), "snapshot_fallback") {
		t.Errorf("log sin evento snapshot_fallback (P7)\nlog: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "catálogo snapshot embebido") {
		t.Errorf("warnings del run sin el substring sintético (P7: binario-added post-Merge)\nlog: %s", buf.String())
	}
}

// ---- B7 (C6, P8) — staleness: warn con edad, exit intacto ----

// TestSnapshotFallback_StalenessWarn congela P8: FetchedAt de hace 31 días
// → warning de staleness en el log con exit intacto (0); FetchedAt de hace
// 5 días → sin warning de staleness.
func TestSnapshotFallback_StalenessWarn(t *testing.T) {
	envKeySet(t)
	newSnapshot := func(days int) func() runOpts {
		return func() runOpts {
			dir := t.TempDir()
			configPath := writeSyncConfig(t, dir, syncConfigTemplate)
			mdPath := filepath.Join(dir, "cache", "modelsdev.json")
			zenPath := filepath.Join(dir, "cache", "zen.json")
			seedZenCache(t, zenPath)
			opts := snapshotOpts(mdPath)
			opts.ConfigPath = configPath
			opts.CachePaths.Zen = zenPath
			opts.Snapshot.FetchedAt = time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
			return opts
		}
	}

	buf, logger := captureLogs()
	if code := run(newSnapshot(31)(), logger); code != 0 {
		t.Fatalf("run con snapshot viejo = %d, want 0 (P8: staleness no cambia exit)", code)
	}
	if !strings.Contains(buf.String(), "stale") {
		t.Errorf("log sin warning de staleness con age 31d (P8)\nlog: %s", buf.String())
	}

	buf2, logger2 := captureLogs()
	if code := run(newSnapshot(5)(), logger2); code != 0 {
		t.Fatalf("run con snapshot fresco = %d, want 0", code)
	}
	if strings.Contains(buf2.String(), "stale") {
		t.Errorf("log CON warning de staleness con age 5d (P8: solo > 30d)\nlog: %s", buf2.String())
	}
}

// ---- B8 (C7, P9/P11) — Available=false ⇒ comportamiento pre-007 ----

// TestLoadSources_NoSnapshotBehavesAsBefore congela P9/P11: sin snapshot
// disponible y sin cache (+ NoFetch) → catalog nil; a nivel run() completo
// → exit 1 (Merge sin fuentes, contrato P12d/P15 de 003/004 intacto).
func TestLoadSources_NoSnapshotBehavesAsBefore(t *testing.T) {
	envKeySet(t)
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "cache", "modelsdev.json")

	opts := snapshotOpts(mdPath)
	opts.Snapshot = runSnapshot{Available: false}
	catalog, _, _, _ := loadSources(opts)
	if catalog != nil {
		t.Errorf("catalog no-nil con Available=false y sin cache (P9: comportamiento pre-007)")
	}

	configPath := writeSyncConfig(t, dir, syncConfigTemplate)
	opts2 := snapshotOpts(mdPath)
	opts2.Snapshot = runSnapshot{Available: false}
	opts2.ConfigPath = configPath
	opts2.CachePaths.Zen = filepath.Join(dir, "cache", "zen.json") // ausente también
	if code := run(opts2, captureLoggerOnly(t)); code != 1 {
		t.Errorf("run sin fuentes = %d, want 1 (P11: fail-loud intacto)", code)
	}
}
