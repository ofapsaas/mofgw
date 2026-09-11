// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package upstream

// Tests de 019-002-fetch-zen-go (spec: docs/specs/019-002-fetch-zen-go/
// spec.md, audit: test-audit.md, bloques B1-B10, postcondiciones P4-P15).
//
// Este archivo usa `package upstream` (no upstream_test) a propósito: el RED
// de esta feature es POR COMPILACIÓN (precedente modelsdev_test.go, 011-005 /
// 015-001 / 019-001) y solo un test file del propio paquete produce los
// `undefined: upstream.*` y `undefined: modelscache.*` esperados con ambos
// paquetes ausentes. El paquete `internal/modelscache` (motor genérico) y
// `internal/upstream` (fuentes zen/go/openrouter) son GREEN (etapa 3.2).
//
// Contrato congelado que estos tests referencian EXACTAMENTE (spec D8):
//
//	modelscache: Spec{BaseURL, Parse, AuthEnv, KnobEnv, Source},
//	Option + WithBaseURL/WithClient/WithLogger, FetchError{Attempt,Type,
//	Status,Message}, ErrFetchDisabled, ErrLockBusy, Fetch[T](ctx, Spec,
//	...Option) (T, []byte, error), DefaultCachePath(filename),
//	DefaultCacheTTL, Store[T]{Path,TTL,Lock}, NewStore[T](Spec, path, ttl,
//	lock, ...Option), Refresh(ctx, force), Get().
//
//	upstream: ZenURL/GoURL/OpenRouterURL, KnobDisableUpstreamFetch,
//	ErrFetchDisabled/ErrLockBusy (aliases del motor, misma identidad
//	errors.Is), DefaultZenCachePath/DefaultGoCachePath/
//	DefaultOpenRouterCachePath, FetchZen/FetchGo/FetchOpenRouter,
//	NewZenStore/NewGoStore/NewOpenRouterStore, ParseModelList,
//	ParseOpenRouterCatalog, ModelList{Object,Models []ModelRef},
//	ModelRef{ID,OwnedBy,Created}, OpenRouterCatalog{Models []OpenRouterModel},
//	OpenRouterModel{ID,CanonicalSlug,Name,ContextLength,Pricing{Prompt,
//	Completion,CacheRead,CacheWrite},SupportedParameters,Architecture{
//	Modality,InputModalities,OutputModalities},TopProvider{ContextLength,
//	MaxCompletionTokens}}.
//
// Los mecanismos del motor (retry/lock/digest/atomic/TTL) NO se re-testean
// aquí por fuente (D7): las clases P4-P16 de 001 ya están suiteadas en
// internal/modelsdev; esta suite ejerce el wiring a través de las fuentes.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/build"
	"github.com/ofapsaas/mofgw/internal/modelscache"
)

// --- Fixtures: recortes fieles al shape real verificado 2026-09-11 (D2) -----

// fixtureZen: OpenAI-list {object, data[]}, items {id, object:"model",
// created(unix), owned_by:"opencode"}. 2 items representativos (70 reales;
// `big-pickle` es un ID real de Zen verificado en el Discovery).
const fixtureZen = `{"object":"list","data":[
  {"id":"qwen3-coder","object":"model","created":1755990000,"owned_by":"opencode"},
  {"id":"big-pickle","object":"model","created":1755903600,"owned_by":"opencode"}
]}`

// fixtureGo: shape idéntico a Zen (D2, 37 reales; `minimax-m3` es un ID real
// de go citado en el spec).
const fixtureGo = `{"object":"list","data":[
  {"id":"minimax-m3","object":"model","created":1756162800,"owned_by":"opencode"},
  {"id":"kimi-k2","object":"model","created":1756076400,"owned_by":"opencode"}
]}`

// fixtureOpenRouter: shape verificado ({data[], ...} rico; `modality` vive en
// `architecture`, NO top-level; pricing strings; aliases `~`). Item completo +
// alias `~` verbatim (I2).
const fixtureOpenRouter = `{"data":[
  {
    "id":"deepseek/deepseek-v4-flash",
    "canonical_slug":"deepseek/deepseek-v4-flash",
    "name":"DeepSeek V4 Flash",
    "created":1750000000,
    "modality":"text->text",
    "context_length":262144,
    "architecture":{"modality":"text->text","input_modalities":["text"],"output_modalities":["text"],"tokenizer":"FakeTokenizer"},
    "top_provider":{"context_length":262144,"max_completion_tokens":65536},
    "pricing":{"prompt":"0.000001","completion":"0.000004","input_cache_read":"0.0000001","input_cache_write":"0.000002"},
    "supported_parameters":["temperature","include_reasoning","reasoning_effort"]
  },
  {
    "id":"~z-ai/glm-latest",
    "canonical_slug":"z-ai/glm-5.2",
    "name":"GLM Latest (alias)",
    "pricing":{"prompt":"0","completion":"0"}
  }
]}`

// fixtureOpenRouterTolerante: item mínimo (sin pricing/architecture/
// top_provider/supported_parameters), item con nulls, y item con los campos
// variables que NO se tipan (P9): benchmarks, per_request_limits,
// pricing.overrides, default_parameters, expiration_date, links.
const fixtureOpenRouterTolerante = `{"data":[
  {"id":"fakecorp/fake-mini","name":"Fake Mini"},
  {"id":"fakecorp/nulos","name":"Nulos","architecture":null,"pricing":null,"top_provider":null},
  {"id":"fakecorp/variable","name":"Variable","benchmarks":{"agieval":0.42},"per_request_limits":{},"default_parameters":{},"expiration_date":"2027-01-01","links":[{"url":"https://example.com"}],"pricing":{"overrides":{}},"supported_parameters":["tools"]}
],"total_count":3,"links":{"next":null}}`

// fixtureOpenRouterPricing: clases string→float64 de P7 — válidos, inválido,
// vacío, null y ausente.
const fixtureOpenRouterPricing = `{"data":[
  {"id":"a/validos","pricing":{"prompt":"0.00001","completion":"0.000004","input_cache_read":"0.0000001","input_cache_write":"0.000002"}},
  {"id":"b/invalidos","pricing":{"prompt":"no-es-numero","completion":"","input_cache_read":null}},
  {"id":"c/sin-pricing"}
]}`

// --- Helpers (precedente modelsdev_test.go, adaptados con captura de auth) ---

// fakeUpstream es un upstream fake que responde según respFn(hit) y registra
// los hits (oráculo "cuántos requests hizo el paquete"), los User-Agent
// recibidos (P4) y los headers Authorization (P10). El contador es atómico
// porque el handler corre en la goroutine del server.
type fakeUpstream struct {
	srv  *httptest.Server
	hits atomic.Int64
	mu   sync.Mutex
	uas  []string
	auth []string
}

func newFakeUpstream(t *testing.T, respFn func(hit int64) (status int, body []byte)) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.hits.Add(1)
		f.mu.Lock()
		f.uas = append(f.uas, r.Header.Get("User-Agent"))
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		status, body := respFn(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) userAgents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.uas...)
}

func (f *fakeUpstream) authorizations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

// constResp devuelve una respFn que responde siempre igual.
func constResp(status int, body []byte) func(int64) (int, []byte) {
	return func(int64) (int, []byte) { return status, body }
}

// fakeOpts apunta cualquier fuente al fake upstream sin depender del
// proxy/entorno (R5/R7 del audit de 001).
func fakeOpts(f *fakeUpstream) []modelscache.Option {
	return []modelscache.Option{modelscache.WithBaseURL(f.srv.URL), modelscache.WithClient(f.srv.Client())}
}

func writeCacheFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write cache fixture: %v", err)
	}
}

// writeSidecar pre-crea el sidecar de digest (D6: <cache-path>.sha256).
func writeSidecar(t *testing.T, path string, body []byte) {
	t.Helper()
	sum := sha256.Sum256(body)
	if err := os.WriteFile(path+".sha256", []byte(hex.EncodeToString(sum[:])), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

// ageCache envejece el mtime del cache SIN sleeps (os.Chtimes, R2 de 001).
func ageCache(t *testing.T, path string, edad time.Duration) {
	t.Helper()
	viejo := time.Now().Add(-edad)
	if err := os.Chtimes(path, viejo, viejo); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// assertUserAgent verifica que el fake recibió UA propio mofgw/<version>.
func assertUserAgent(t *testing.T, f *fakeUpstream) {
	t.Helper()
	uas := f.userAgents()
	if len(uas) == 0 {
		t.Fatal("el fake no recibió ningún request")
	}
	ua := uas[0]
	if ua != build.UserAgent {
		t.Fatalf("User-Agent = %q, want %q", ua, build.UserAgent)
	}
	if !strings.HasPrefix(ua, "mofgw/") || strings.HasPrefix(ua, "Go-http-client") {
		t.Fatalf("User-Agent %q no es mofgw/<version>", ua)
	}
}

// parseMapFixture es el parser trivial para specs compuestas en tests del
// motor (B7/B9): decodifica el body como objeto JSON genérico.
func parseMapFixture(b []byte) (any, error) {
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// captureLogs / logRecords / findEvent / assertFields: precedente
// modelsdev_test.go:911-953 (handler slog JSON capturador).
func captureLogs() (*bytes.Buffer, *slog.Logger) {
	buf := &bytes.Buffer{}
	return buf, slog.New(slog.NewJSONHandler(buf, nil))
}

func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("línea de log no es JSON: %q", line)
		}
		recs = append(recs, rec)
	}
	return recs
}

func findEvent(t *testing.T, recs []map[string]any, event string) map[string]any {
	t.Helper()
	for _, rec := range recs {
		if rec["msg"] == event {
			return rec
		}
	}
	t.Fatalf("evento %q no encontrado en logs: %v", event, recs)
	return nil
}

func assertSource(t *testing.T, rec map[string]any, want string) {
	t.Helper()
	if v, ok := rec["source"].(string); !ok || v != want {
		t.Fatalf("source = %v, want %q (P14/D9)", rec["source"], want)
	}
}

// --- B1: P4, P5 — FetchZen/FetchGo fieles por índice -------------------------

func TestFetchZen_FaithfulList(t *testing.T) {
	if ZenURL != "https://opencode.ai/zen/v1/models" {
		t.Fatalf("ZenURL = %q, want https://opencode.ai/zen/v1/models (D3)", ZenURL)
	}
	f := newFakeUpstream(t, constResp(200, []byte(fixtureZen)))

	list, raw, err := FetchZen(context.Background(), fakeOpts(f)...)
	if err != nil {
		t.Fatalf("FetchZen: %v", err)
	}
	if list.Object != "list" {
		t.Fatalf("Object = %q, want %q", list.Object, "list")
	}
	if len(list.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(list.Models))
	}
	// Fiel por índice (P4): Models[i] = item i de data[].
	want := []ModelRef{
		{ID: "qwen3-coder", OwnedBy: "opencode", Created: 1755990000},
		{ID: "big-pickle", OwnedBy: "opencode", Created: 1755903600},
	}
	for i, m := range list.Models {
		if m != want[i] {
			t.Fatalf("Models[%d] = %+v, want %+v (fiel por índice)", i, m, want[i])
		}
	}
	// raw byte-idéntico al servido (P4): el cache queda fiel al upstream.
	if !bytes.Equal(raw, []byte(fixtureZen)) {
		t.Fatalf("raw no es byte-idéntico al body servido (len raw=%d, want %d)", len(raw), len(fixtureZen))
	}
	// User-Agent propio (P4/I7).
	assertUserAgent(t, f)
}

func TestFetchGo_FaithfulList(t *testing.T) {
	if GoURL != "https://opencode.ai/zen/go/v1/models" {
		t.Fatalf("GoURL = %q, want https://opencode.ai/zen/go/v1/models (D3)", GoURL)
	}
	f := newFakeUpstream(t, constResp(200, []byte(fixtureGo)))

	list, raw, err := FetchGo(context.Background(), fakeOpts(f)...)
	if err != nil {
		t.Fatalf("FetchGo: %v", err)
	}
	if list.Object != "list" {
		t.Fatalf("Object = %q, want %q", list.Object, "list")
	}
	if len(list.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(list.Models))
	}
	want := []ModelRef{
		{ID: "minimax-m3", OwnedBy: "opencode", Created: 1756162800},
		{ID: "kimi-k2", OwnedBy: "opencode", Created: 1756076400},
	}
	for i, m := range list.Models {
		if m != want[i] {
			t.Fatalf("Models[%d] = %+v, want %+v (fiel por índice)", i, m, want[i])
		}
	}
	if !bytes.Equal(raw, []byte(fixtureGo)) {
		t.Fatalf("raw no es byte-idéntico al body servido (len raw=%d, want %d)", len(raw), len(fixtureGo))
	}
	assertUserAgent(t, f)
}

// --- B2: P6 — parseo ModelList tolerante --------------------------------------

func TestParseModelList_Tolerant(t *testing.T) {
	t.Run("data_ausente_object_cero", func(t *testing.T) {
		// P6: data ausente → Models nil sin error; object top-level ausente
		// → zero-value.
		list, err := ParseModelList([]byte(`{}`))
		if err != nil {
			t.Fatalf("ParseModelList sin data: %v", err)
		}
		if list.Models != nil {
			t.Fatalf("Models = %v, want nil (data ausente)", list.Models)
		}
		if list.Object != "" {
			t.Fatalf("Object = %q, want zero-value (campo ausente)", list.Object)
		}
	})

	t.Run("data_null", func(t *testing.T) {
		list, err := ParseModelList([]byte(`{"object":"list","data":null}`))
		if err != nil {
			t.Fatalf("ParseModelList con data null: %v", err)
		}
		if list.Models != nil {
			t.Fatalf("Models = %v, want nil (data null)", list.Models)
		}
		if list.Object != "list" {
			t.Fatalf("Object = %q, want list", list.Object)
		}
	})

	t.Run("extras_ignorados", func(t *testing.T) {
		// P6: items con campos extra desconocidos → ignorados sin error.
		raw := `{"object":"list","data":[
			{"id":"a/b","object":"model","created":1,"owned_by":"opencode","campo_futuro":{"x":1},"otro":[1,2]}
		]}`
		list, err := ParseModelList([]byte(raw))
		if err != nil {
			t.Fatalf("ParseModelList con extras: %v", err)
		}
		if len(list.Models) != 1 {
			t.Fatalf("models = %d, want 1", len(list.Models))
		}
		if m := list.Models[0]; m.ID != "a/b" || m.OwnedBy != "opencode" || m.Created != 1 {
			t.Fatalf("Models[0] = %+v, want fiel a pesar de extras", m)
		}
	})

	t.Run("json_invalido_error", func(t *testing.T) {
		if _, err := ParseModelList([]byte(`{"object":`)); err == nil {
			t.Fatal("JSON inválido no surfaced como error")
		}
	})
}

// --- B3: P7 — pricing OpenRouter string→float64 -------------------------------

func TestParseOpenRouterCatalog_PricingStrings(t *testing.T) {
	cat, err := ParseOpenRouterCatalog([]byte(fixtureOpenRouterPricing))
	if err != nil {
		t.Fatalf("ParseOpenRouterCatalog: %v (la tolerancia de pricing no debe fallar)", err)
	}
	if len(cat.Models) != 3 {
		t.Fatalf("models = %d, want 3", len(cat.Models))
	}
	// Válidos: strings → float64, mapeo 1:1 con input_cache_* (P7).
	p := cat.Models[0].Pricing
	if p.Prompt != 0.00001 || p.Completion != 0.000004 || p.CacheRead != 0.0000001 || p.CacheWrite != 0.000002 {
		t.Fatalf("pricing = %v/%v/%v/%v, want 0.00001/0.000004/0.0000001/0.000002",
			p.Prompt, p.Completion, p.CacheRead, p.CacheWrite)
	}
	// No numérico, vacío y null → 0 sin error (P7).
	p = cat.Models[1].Pricing
	if p.Prompt != 0 {
		t.Fatalf("prompt no numérico = %v, want 0", p.Prompt)
	}
	if p.Completion != 0 {
		t.Fatalf("completion vacío = %v, want 0", p.Completion)
	}
	if p.CacheRead != 0 {
		t.Fatalf("input_cache_read null = %v, want 0", p.CacheRead)
	}
	if p.CacheWrite != 0 {
		t.Fatalf("input_cache_write ausente = %v, want 0", p.CacheWrite)
	}
	// Item sin pricing entero → zero-value sin error (P7/P9).
	p = cat.Models[2].Pricing
	if p != (OpenRouterModel{}.Pricing) {
		t.Fatalf("pricing de item sin pricing = %+v, want zero-value", p)
	}
}

// --- B4: P8, P9 — campos fieles + aliases + schema variable --------------------

func TestParseOpenRouterCatalog_FaithfulFields(t *testing.T) {
	cat, err := ParseOpenRouterCatalog([]byte(fixtureOpenRouter))
	if err != nil {
		t.Fatalf("ParseOpenRouterCatalog: %v", err)
	}
	if len(cat.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(cat.Models))
	}
	m := cat.Models[0]
	// Campos fieles tal cual upstream (P8/I2).
	if m.ID != "deepseek/deepseek-v4-flash" {
		t.Fatalf("ID = %q, want deepseek/deepseek-v4-flash", m.ID)
	}
	if m.CanonicalSlug != "deepseek/deepseek-v4-flash" {
		t.Fatalf("CanonicalSlug = %q, want deepseek/deepseek-v4-flash", m.CanonicalSlug)
	}
	if m.Name != "DeepSeek V4 Flash" {
		t.Fatalf("Name = %q, want DeepSeek V4 Flash", m.Name)
	}
	if m.ContextLength != 262144 {
		t.Fatalf("ContextLength = %d, want 262144", m.ContextLength)
	}
	if m.TopProvider.ContextLength != 262144 || m.TopProvider.MaxCompletionTokens != 65536 {
		t.Fatalf("TopProvider = %d/%d, want 262144/65536", m.TopProvider.ContextLength, m.TopProvider.MaxCompletionTokens)
	}
	// La modality vive en architecture, NO top-level (D2: el "modality"
	// top-level del fixture debe ignorarse).
	if m.Architecture.Modality != "text->text" {
		t.Fatalf("Architecture.Modality = %q, want text->text", m.Architecture.Modality)
	}
	if len(m.Architecture.InputModalities) != 1 || m.Architecture.InputModalities[0] != "text" {
		t.Fatalf("Architecture.InputModalities = %v, want [text]", m.Architecture.InputModalities)
	}
	if len(m.Architecture.OutputModalities) != 1 || m.Architecture.OutputModalities[0] != "text" {
		t.Fatalf("Architecture.OutputModalities = %v, want [text]", m.Architecture.OutputModalities)
	}
	// supported_parameters tal cual, sin normalización de vocabulario (I2/R5).
	wantParams := []string{"temperature", "include_reasoning", "reasoning_effort"}
	if len(m.SupportedParameters) != len(wantParams) {
		t.Fatalf("SupportedParameters = %v, want %v", m.SupportedParameters, wantParams)
	}
	for i, p := range m.SupportedParameters {
		if p != wantParams[i] {
			t.Fatalf("SupportedParameters[%d] = %q, want %q (tal cual, I2)", i, p, wantParams[i])
		}
	}
	// Pricing strings del item completo.
	p := m.Pricing
	if p.Prompt != 0.000001 || p.Completion != 0.000004 || p.CacheRead != 0.0000001 || p.CacheWrite != 0.000002 {
		t.Fatalf("Pricing = %v/%v/%v/%v, want 0.000001/0.000004/0.0000001/0.000002",
			p.Prompt, p.Completion, p.CacheRead, p.CacheWrite)
	}
	// Alias `~` incluido TAL CUAL (P8/I2: sin resolución, eso es 019-003).
	alias := cat.Models[1]
	if alias.ID != "~z-ai/glm-latest" {
		t.Fatalf("alias.ID = %q, want ~z-ai/glm-latest verbatim", alias.ID)
	}
	if alias.CanonicalSlug != "z-ai/glm-5.2" {
		t.Fatalf("alias.CanonicalSlug = %q, want z-ai/glm-5.2", alias.CanonicalSlug)
	}
}

func TestParseOpenRouterCatalog_TolerantShape(t *testing.T) {
	cat, err := ParseOpenRouterCatalog([]byte(fixtureOpenRouterTolerante))
	// P9: items sin pricing/architecture/top_provider (o null) → zero-values
	// sin error; campos variables ignorados sin error.
	if err != nil {
		t.Fatalf("ParseOpenRouterCatalog de schema variable: %v", err)
	}
	if len(cat.Models) != 3 {
		t.Fatalf("models = %d, want 3", len(cat.Models))
	}
	// Item mínimo: TODO zero-value.
	m := cat.Models[0]
	if m.ContextLength != 0 {
		t.Fatalf("ContextLength ausente = %d, want 0 (P9)", m.ContextLength)
	}
	if m.Pricing != (OpenRouterModel{}.Pricing) {
		t.Fatalf("Pricing ausente = %+v, want zero-value (P9)", m.Pricing)
	}
	// Architecture contiene slices: comparación campo a campo (no comparable
	// con != en Go).
	if m.Architecture.Modality != "" || len(m.Architecture.InputModalities) != 0 || len(m.Architecture.OutputModalities) != 0 {
		t.Fatalf("Architecture ausente = %+v, want zero-value (P9)", m.Architecture)
	}
	if m.TopProvider != (OpenRouterModel{}.TopProvider) {
		t.Fatalf("TopProvider ausente = %+v, want zero-value (P9)", m.TopProvider)
	}
	if len(m.SupportedParameters) != 0 {
		t.Fatalf("SupportedParameters ausente = %v, want vacío", m.SupportedParameters)
	}
	// Item con nulls explícitos: también zero-value sin error.
	m = cat.Models[1]
	if m.Pricing != (OpenRouterModel{}.Pricing) {
		t.Fatalf("pricing null = %+v, want zero-value sin error (P9)", m.Pricing)
	}
	if m.Architecture.Modality != "" || len(m.Architecture.InputModalities) != 0 || len(m.Architecture.OutputModalities) != 0 {
		t.Fatalf("architecture null = %+v, want zero-value sin error (P9)", m.Architecture)
	}
	if m.TopProvider != (OpenRouterModel{}.TopProvider) {
		t.Fatalf("top_provider null = %+v, want zero-value sin error (P9)", m.TopProvider)
	}
	// Item con schema variable (benchmarks/per_request_limits/overrides/
	// default_parameters/expiration_date/links): ignorados sin error (P9/I2).
	m = cat.Models[2]
	if len(m.SupportedParameters) != 1 || m.SupportedParameters[0] != "tools" {
		t.Fatalf("SupportedParameters = %v, want [tools] tal cual", m.SupportedParameters)
	}
	if m.Pricing != (OpenRouterModel{}.Pricing) {
		t.Fatalf("pricing con solo overrides = %+v, want zero-value (overrides no se tipa)", m.Pricing)
	}
	if _, err := ParseOpenRouterCatalog([]byte(`{"data":[{`)); err == nil {
		t.Fatal("JSON inválido no surfaced como error")
	}
}

// --- B5: P10 — auth condicional OpenRouter (sin t.Parallel: t.Setenv) --------

const authEnv = "OPENROUTER_API_KEY" // D4: mismo nombre que usa el skill

func TestFetchOpenRouter_AuthConditional(t *testing.T) {
	if OpenRouterURL != "https://openrouter.ai/api/v1/models" {
		t.Fatalf("OpenRouterURL = %q, want https://openrouter.ai/api/v1/models (D3)", OpenRouterURL)
	}
	ctx := context.Background()

	t.Run("con_env_lleva_bearer", func(t *testing.T) {
		t.Setenv(authEnv, "sk-or-test-secreto-42")
		f := newFakeUpstream(t, constResp(200, []byte(fixtureOpenRouter)))
		cat, _, err := FetchOpenRouter(ctx, fakeOpts(f)...)
		if err != nil {
			t.Fatalf("FetchOpenRouter con auth: %v", err)
		}
		if len(cat.Models) != 2 {
			t.Fatalf("models = %d, want 2", len(cat.Models))
		}
		auths := f.authorizations()
		if len(auths) != 1 || auths[0] != "Bearer sk-or-test-secreto-42" {
			t.Fatalf("Authorization = %v, want [Bearer sk-or-test-secreto-42]", auths)
		}
	})

	t.Run("sin_env_anonimo", func(t *testing.T) {
		t.Setenv(authEnv, "") // registra el cleanup; ausente real:
		if err := os.Unsetenv(authEnv); err != nil {
			t.Fatalf("unset env: %v", err)
		}
		f := newFakeUpstream(t, constResp(200, []byte(fixtureOpenRouter)))
		_, _, err := FetchOpenRouter(ctx, fakeOpts(f)...)
		if err != nil {
			t.Fatalf("FetchOpenRouter anónimo: %v (el catálogo anónimo está verificado completo)", err)
		}
		auths := f.authorizations()
		if len(auths) != 1 || auths[0] != "" {
			t.Fatalf("Authorization = %v, want header ausente (anónimo)", auths)
		}
		// Anónimo = solo UA (P10/I7).
		assertUserAgent(t, f)
	})

	t.Run("env_vacia_anonimo", func(t *testing.T) {
		t.Setenv(authEnv, "") // P10: vacía → anónimo
		f := newFakeUpstream(t, constResp(200, []byte(fixtureOpenRouter)))
		_, _, err := FetchOpenRouter(ctx, fakeOpts(f)...)
		if err != nil {
			t.Fatalf("FetchOpenRouter con env vacía: %v", err)
		}
		auths := f.authorizations()
		if len(auths) != 1 || auths[0] != "" {
			t.Fatalf("Authorization = %v, want header ausente (env vacía)", auths)
		}
	})

	t.Run("lectura_por_llamada", func(t *testing.T) {
		// D4: la env se lee POR LLAMADA (precedente fetchDisabled).
		f := newFakeUpstream(t, constResp(200, []byte(fixtureOpenRouter)))
		t.Setenv(authEnv, "sk-primera")
		if _, _, err := FetchOpenRouter(ctx, fakeOpts(f)...); err != nil {
			t.Fatalf("FetchOpenRouter primera llamada: %v", err)
		}
		t.Setenv(authEnv, "sk-segunda")
		if _, _, err := FetchOpenRouter(ctx, fakeOpts(f)...); err != nil {
			t.Fatalf("FetchOpenRouter segunda llamada: %v", err)
		}
		auths := f.authorizations()
		if len(auths) != 2 {
			t.Fatalf("auths capturadas = %d, want 2", len(auths))
		}
		if auths[0] != "Bearer sk-primera" || auths[1] != "Bearer sk-segunda" {
			t.Fatalf("Authorization = %v/%v, want Bearer sk-primera/sk-segunda (por llamada)", auths[0], auths[1])
		}
	})
}

// --- B6: P11 — el secreto jamás en logs ni errores -----------------------------

func TestOpenRouter_KeyNeverLogged(t *testing.T) {
	const key = "sk-or-secreto-super-privado-42"
	t.Setenv(authEnv, key)
	buf, logger := captureLogs()
	// 401 con auth fallida: el error menciona el status, nunca la key (P11).
	f := newFakeUpstream(t, constResp(401, []byte(`{"error":{"message":"Invalid key"}}`)))
	_, _, err := FetchOpenRouter(context.Background(), append(fakeOpts(f), modelscache.WithLogger(logger))...)
	if err == nil {
		t.Fatal("401 no surfaced como error")
	}
	// P11: el valor de la key no aparece en NINGÚN evento de log capturado.
	if s := buf.String(); strings.Contains(s, key) {
		t.Fatalf("la key apareció en los logs: %.200s", s)
	}
	// ...ni en el texto de ningún error del paquete.
	if msg := err.Error(); strings.Contains(msg, key) {
		t.Fatalf("err.Error() contiene la key: %v", msg)
	}
	// El error menciona el status (401), nunca la key (P11).
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want que mencione el status 401", err)
	}
	// Clasificación del motor congelada (D8: FetchError idéntico a 001).
	var fe *modelscache.FetchError
	if !errors.As(err, &fe) || fe.Status != 401 {
		t.Fatalf("err = %v (%T), want *modelscache.FetchError con Status 401", err, err)
	}
	// 4xx no reintenta (clase del motor): 1 solo request.
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1 (4xx no reintenta)", n)
	}
}

// --- B7: P12 — knob de fuentes aislado (oráculo = contador de hits) ------------

// assertKnobStoreRefresh ejercita un Store de fuente con el knob activo
// (t.Setenv ya hecho por el caller). withCache=false también verifica que no
// se crea cache file. Oráculo SIEMPRE hits==0 (riesgo (a) del audit: jamás
// errors.Is cruzado entre paquetes como oráculo de aislamiento).
func assertKnobStoreRefresh[M any](
	t *testing.T,
	path string,
	fixture []byte,
	withCache bool,
	newStore func(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[M],
) {
	t.Helper()
	f := newFakeUpstream(t, constResp(200, fixture))
	if withCache {
		writeCacheFile(t, path, fixture)
	}
	store := newStore(path, modelscache.DefaultCacheTTL, false, fakeOpts(f)...)
	// force=true: con knob activo y cache presente el cache se sirve sin fetch.
	changed, err := store.Refresh(context.Background(), true)
	if withCache {
		if err != nil {
			t.Fatalf("Refresh con knob activo y cache presente: %v, want (false, nil)", err)
		}
		if changed {
			t.Fatal("changed = true con knob activo, want false")
		}
	} else {
		if err == nil || !errors.Is(err, ErrFetchDisabled) {
			t.Fatalf("err = %v, want ErrFetchDisabled (sin cache y knob activo)", err)
		}
		if changed {
			t.Fatal("changed = true con knob activo y sin cache, want false")
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatal("se creó cache file con knob activo y sin cache previo")
		}
	}
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("hits = %d, want 0 (knob activo no emite requests)", n)
	}
}

func TestUpstreamKnob_DisablesSources(t *testing.T) {
	if KnobDisableUpstreamFetch != "MOFGW_DISABLE_UPSTREAM_FETCH" {
		t.Fatalf("KnobDisableUpstreamFetch = %q, want MOFGW_DISABLE_UPSTREAM_FETCH (D6)", KnobDisableUpstreamFetch)
	}
	ctx := context.Background()
	knob := KnobDisableUpstreamFetch

	t.Run("fetch_zen", func(t *testing.T) {
		t.Setenv(knob, "1")
		f := newFakeUpstream(t, constResp(200, []byte(fixtureZen)))
		_, _, err := FetchZen(ctx, fakeOpts(f)...)
		if err == nil || !errors.Is(err, ErrFetchDisabled) {
			t.Fatalf("err = %v, want ErrFetchDisabled (via errors.Is)", err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("hits = %d, want 0", n)
		}
	})

	t.Run("fetch_go", func(t *testing.T) {
		t.Setenv(knob, "1")
		f := newFakeUpstream(t, constResp(200, []byte(fixtureGo)))
		_, _, err := FetchGo(ctx, fakeOpts(f)...)
		if err == nil || !errors.Is(err, ErrFetchDisabled) {
			t.Fatalf("err = %v, want ErrFetchDisabled (via errors.Is)", err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("hits = %d, want 0", n)
		}
	})

	t.Run("fetch_openrouter", func(t *testing.T) {
		t.Setenv(knob, "1")
		f := newFakeUpstream(t, constResp(200, []byte(fixtureOpenRouter)))
		_, _, err := FetchOpenRouter(ctx, fakeOpts(f)...)
		if err == nil || !errors.Is(err, ErrFetchDisabled) {
			t.Fatalf("err = %v, want ErrFetchDisabled (via errors.Is)", err)
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("hits = %d, want 0", n)
		}
	})

	t.Run("store_zen_con_cache_sirve", func(t *testing.T) {
		t.Setenv(knob, "1")
		assertKnobStoreRefresh(t, filepath.Join(t.TempDir(), "zen-models.json"), []byte(fixtureZen), true, NewZenStore)
	})

	t.Run("store_go_con_cache_sirve", func(t *testing.T) {
		t.Setenv(knob, "1")
		assertKnobStoreRefresh(t, filepath.Join(t.TempDir(), "go-models.json"), []byte(fixtureGo), true, NewGoStore)
	})

	t.Run("store_openrouter_con_cache_sirve", func(t *testing.T) {
		t.Setenv(knob, "1")
		assertKnobStoreRefresh(t, filepath.Join(t.TempDir(), "openrouter-models.json"), []byte(fixtureOpenRouter), true, NewOpenRouterStore)
	})

	t.Run("store_zen_sin_cache_falla", func(t *testing.T) {
		t.Setenv(knob, "1")
		assertKnobStoreRefresh(t, filepath.Join(t.TempDir(), "zen-models.json"), []byte(fixtureZen), false, NewZenStore)
	})

	t.Run("store_go_sin_cache_falla", func(t *testing.T) {
		t.Setenv(knob, "1")
		assertKnobStoreRefresh(t, filepath.Join(t.TempDir(), "go-models.json"), []byte(fixtureGo), false, NewGoStore)
	})

	t.Run("store_openrouter_sin_cache_falla", func(t *testing.T) {
		t.Setenv(knob, "1")
		assertKnobStoreRefresh(t, filepath.Join(t.TempDir(), "openrouter-models.json"), []byte(fixtureOpenRouter), false, NewOpenRouterStore)
	})

	t.Run("knob_0_y_vacio_son_comportamiento_normal", func(t *testing.T) {
		// P13/I8 de 001: "0" y "" → fetch-on.
		for _, v := range []string{"0", ""} {
			t.Setenv(knob, v)
			f := newFakeUpstream(t, constResp(200, []byte(fixtureZen)))
			list, _, err := FetchZen(ctx, fakeOpts(f)...)
			if err != nil {
				t.Fatalf("FetchZen con knob=%q: %v, want comportamiento normal", v, err)
			}
			if list.Object != "list" {
				t.Fatalf("Object = %q con knob=%q, want lista fiel", list.Object, v)
			}
			if n := f.hits.Load(); n != 1 {
				t.Fatalf("hits = %d con knob=%q, want 1", n, v)
			}
		}
	})

	t.Run("aliases_del_motor", func(t *testing.T) {
		// D8: los errores de upstream son los del motor, misma identidad
		// errors.Is (en AMBAS direcciones: misma var, no wrapper).
		if ErrFetchDisabled == nil || ErrLockBusy == nil {
			t.Fatalf("errores nil: %v/%v", ErrFetchDisabled, ErrLockBusy)
		}
		if !errors.Is(ErrFetchDisabled, modelscache.ErrFetchDisabled) {
			t.Fatalf("ErrFetchDisabled no comparable con modelscache.ErrFetchDisabled: %v", ErrFetchDisabled)
		}
		if !errors.Is(modelscache.ErrFetchDisabled, ErrFetchDisabled) {
			t.Fatal("identidad de ErrFetchDisabled no simétrica (wrapper en vez de alias)")
		}
		if !errors.Is(ErrLockBusy, modelscache.ErrLockBusy) {
			t.Fatalf("ErrLockBusy no comparable con modelscache.ErrLockBusy: %v", ErrLockBusy)
		}
		if !errors.Is(modelscache.ErrLockBusy, ErrLockBusy) {
			t.Fatal("identidad de ErrLockBusy no simétrica (wrapper en vez de alias)")
		}
	})
}

func TestUpstreamKnob_Isolation(t *testing.T) {
	ctx := context.Background()

	t.Run("knob_upstream_no_afecta_spec_modelsdev", func(t *testing.T) {
		// Con el knob de 002 ACTIVO, un Spec con el KnobEnv de modelsdev
		// fetchea igual (I13/D6: cada Spec lleva su KnobEnv).
		t.Setenv(KnobDisableUpstreamFetch, "1")
		f := newFakeUpstream(t, constResp(200, []byte(`{"ok":true}`)))
		spec := modelscache.Spec{
			BaseURL: f.srv.URL,
			Parse:   parseMapFixture,
			KnobEnv: "MOFGW_DISABLE_MODELS_FETCH", // NO seteado
			Source:  "",
		}
		if _, _, err := modelscache.Fetch[map[string]any](ctx, spec, modelscache.WithClient(f.srv.Client())); err != nil {
			t.Fatalf("Fetch con knob de upstream activo: %v, want comportamiento normal", err)
		}
		if n := f.hits.Load(); n != 1 {
			t.Fatalf("hits = %d, want 1 (el knob de 002 no afecta otro Spec)", n)
		}
	})

	t.Run("knob_modelsdev_no_afecta_fuentes", func(t *testing.T) {
		t.Setenv("MOFGW_DISABLE_MODELS_FETCH", "1")
		f := newFakeUpstream(t, constResp(200, []byte(fixtureZen)))
		list, _, err := FetchZen(ctx, fakeOpts(f)...)
		if err != nil {
			t.Fatalf("FetchZen con knob de modelsdev activo: %v, want comportamiento normal", err)
		}
		if list.Object != "list" {
			t.Fatalf("Object = %q, want lista fiel", list.Object)
		}
		if n := f.hits.Load(); n != 1 {
			t.Fatalf("hits = %d, want 1", n)
		}
	})
}

// --- B8: P13 — paths default por fuente + aislamiento de stores ----------------

func TestDefaultCachePaths_PerSource(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOFGW_CACHE_DIR", dir) // regla D5: MOFGW_CACHE_DIR primero
	zp := DefaultZenCachePath()
	gp := DefaultGoCachePath()
	op := DefaultOpenRouterCachePath()
	if filepath.Dir(zp) != dir || filepath.Base(zp) != "zen-models.json" {
		t.Fatalf("DefaultZenCachePath = %q, want %q", zp, filepath.Join(dir, "zen-models.json"))
	}
	if filepath.Dir(gp) != dir || filepath.Base(gp) != "go-models.json" {
		t.Fatalf("DefaultGoCachePath = %q, want %q", gp, filepath.Join(dir, "go-models.json"))
	}
	if filepath.Dir(op) != dir || filepath.Base(op) != "openrouter-models.json" {
		t.Fatalf("DefaultOpenRouterCachePath = %q, want %q", op, filepath.Join(dir, "openrouter-models.json"))
	}
	// Tres archivos DISTINTOS (D5: caches separados por fuente).
	if zp == gp || zp == op || gp == op {
		t.Fatalf("paths colisionan: %q / %q / %q", zp, gp, op)
	}

	t.Run("new_store_defaults", func(t *testing.T) {
		// D8: path vacío en New*Store → Default*CachePath(); ttl <= 0 →
		// modelscache.DefaultCacheTTL. Construir no fetchea (hits==0).
		f := newFakeUpstream(t, constResp(200, []byte(fixtureZen)))
		zs := NewZenStore("", 0, false, modelscache.WithClient(f.srv.Client()))
		if zs.Path != zp {
			t.Fatalf("NewZenStore(\"\").Path = %q, want %q (default por fuente)", zs.Path, zp)
		}
		if zs.TTL != modelscache.DefaultCacheTTL {
			t.Fatalf("NewZenStore(0).TTL = %v, want %v", zs.TTL, modelscache.DefaultCacheTTL)
		}
		gs := NewGoStore("", 0, false, modelscache.WithClient(f.srv.Client()))
		if gs.Path != gp || gs.TTL != modelscache.DefaultCacheTTL {
			t.Fatalf("NewGoStore defaults = %q/%v, want %q/%v", gs.Path, gs.TTL, gp, modelscache.DefaultCacheTTL)
		}
		ors := NewOpenRouterStore("", 0, false, modelscache.WithClient(f.srv.Client()))
		if ors.Path != op {
			t.Fatalf("NewOpenRouterStore(\"\").Path = %q, want %q", ors.Path, op)
		}
		// El motor generaliza DefaultCachePath(filename) (D5).
		if got := modelscache.DefaultCachePath("custom.json"); filepath.Dir(got) != dir || filepath.Base(got) != "custom.json" {
			t.Fatalf("modelscache.DefaultCachePath = %q, want %q", got, filepath.Join(dir, "custom.json"))
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("hits = %d, want 0 (construir no fetchea)", n)
		}
	})
}

func TestStores_Isolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// Mitigación (d) del audit: paths EXPLÍCITOS por fuente en subdirs de
	// t.TempDir; Default*CachePath jamás dentro de este test.
	zenPath := filepath.Join(dir, "zen", "zen-models.json")
	goPath := filepath.Join(dir, "go", "go-models.json")
	orPath := filepath.Join(dir, "or", "openrouter-models.json")

	// Caches Ajenos corruptos en disco (P13: corrupción cruzada sin efecto).
	writeCacheFile(t, goPath, []byte(`{"corrupto`))
	writeCacheFile(t, orPath, []byte(`{{{no-json`))
	goBefore := readBytes(t, goPath)
	orBefore := readBytes(t, orPath)

	// Fakes separados por fuente: cada store pega SOLO al suyo.
	fz := newFakeUpstream(t, constResp(200, []byte(fixtureZen)))
	fg := newFakeUpstream(t, constResp(200, []byte(fixtureGo)))
	fo := newFakeUpstream(t, constResp(200, []byte(fixtureOpenRouter)))

	zs := NewZenStore(zenPath, modelscache.DefaultCacheTTL, true, fakeOpts(fz)...)
	changed, err := zs.Refresh(ctx, false)
	if err != nil {
		t.Fatalf("Refresh de zen con caches ajenos corruptos: %v, want sin error (P13)", err)
	}
	if !changed {
		t.Fatal("changed = false en primer write, want true")
	}
	if n := fz.hits.Load(); n != 1 {
		t.Fatalf("hits de zen = %d, want 1", n)
	}
	// Solo zen fetchea: los fakes de go/openrouter sin hits (P13).
	if n := fg.hits.Load(); n != 0 {
		t.Fatalf("hits de go = %d, want 0", n)
	}
	if n := fo.hits.Load(); n != 0 {
		t.Fatalf("hits de openrouter = %d, want 0", n)
	}
	// Los caches ajenos quedan byte-intactos y SIN sidecar/lock nuevos.
	if !bytes.Equal(readBytes(t, goPath), goBefore) {
		t.Fatal("el refresh de zen modificó el cache de go")
	}
	if !bytes.Equal(readBytes(t, orPath), orBefore) {
		t.Fatal("el refresh de zen modificó el cache de openrouter")
	}
	if _, statErr := os.Stat(goPath + ".lock"); !os.IsNotExist(statErr) {
		t.Fatal("el refresh de zen creó lock file del store de go")
	}
	if _, statErr := os.Stat(orPath + ".sha256"); !os.IsNotExist(statErr) {
		t.Fatal("el refresh de zen creó sidecar del store de openrouter")
	}
	// El lock/sidecar PROPIOS de zen sí existen (lock file dedicado, I10).
	if _, statErr := os.Stat(zenPath + ".sha256"); statErr != nil {
		t.Fatalf("sidecar propio de zen no creado: %v", statErr)
	}
	if _, statErr := os.Stat(zenPath + ".lock"); statErr != nil {
		t.Fatalf("lock file propio de zen no creado: %v", statErr)
	}
}

// --- B9: P14 — atributo source en logs del motor (condicional a Spec.Source) ---

func TestEngineLog_SourceAttribute(t *testing.T) {
	ctx := context.Background()

	t.Run("fetch_ok_zen", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, []byte(fixtureZen)))
		if _, _, err := FetchZen(ctx, append(fakeOpts(f), modelscache.WithLogger(logger))...); err != nil {
			t.Fatalf("FetchZen: %v", err)
		}
		assertSource(t, findEvent(t, logRecords(t, buf), "fetch_ok"), "zen")
	})

	t.Run("fetch_ok_go", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, []byte(fixtureGo)))
		if _, _, err := FetchGo(ctx, append(fakeOpts(f), modelscache.WithLogger(logger))...); err != nil {
			t.Fatalf("FetchGo: %v", err)
		}
		assertSource(t, findEvent(t, logRecords(t, buf), "fetch_ok"), "go")
	})

	t.Run("fetch_ok_openrouter", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, []byte(fixtureOpenRouter)))
		if _, _, err := FetchOpenRouter(ctx, append(fakeOpts(f), modelscache.WithLogger(logger))...); err != nil {
			t.Fatalf("FetchOpenRouter: %v", err)
		}
		assertSource(t, findEvent(t, logRecords(t, buf), "fetch_ok"), "openrouter")
	})

	t.Run("cache_hit_zen", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, []byte(fixtureZen))) // no debe pegarle
		path := filepath.Join(t.TempDir(), "zen-models.json")
		writeCacheFile(t, path, []byte(fixtureZen))
		store := NewZenStore(path, modelscache.DefaultCacheTTL, false,
			append(fakeOpts(f), modelscache.WithLogger(logger))...)
		if _, err := store.Get(); err != nil {
			t.Fatalf("Get: %v", err)
		}
		assertSource(t, findEvent(t, logRecords(t, buf), "cache_hit"), "zen")
	})

	t.Run("modelsdev_sin_source", func(t *testing.T) {
		// Complemento negativo del gate P17 de 001 (riesgo (b) del audit):
		// Spec.Source == "" → NINGÚN record lleva el atributo "source"
		// (los logs de modelsdev deben quedar byte-idénticos a los de 001).
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, []byte(`{"ok":true}`)))
		spec := modelscache.Spec{
			BaseURL: f.srv.URL,
			Parse:   parseMapFixture,
			KnobEnv: "MOFGW_DISABLE_MODELS_FETCH",
			Source:  "",
		}
		if _, _, err := modelscache.Fetch[map[string]any](ctx, spec, modelscache.WithClient(f.srv.Client()), modelscache.WithLogger(logger)); err != nil {
			t.Fatalf("modelscache.Fetch: %v", err)
		}
		recs := logRecords(t, buf)
		findEvent(t, recs, "fetch_ok") // hubo actividad logueada
		for i, rec := range recs {
			if _, ok := rec["source"]; ok {
				t.Fatalf("record %d (msg=%v) lleva atributo source con Spec.Source vacío", i, rec["msg"])
			}
		}
	})
}

// --- B10: P15 — wiring del Store por fuente a través del motor ------------------

// runStoreWiring ejercita las clases P15 PARA UNA FUENTE: fetch+persist+
// changed con cache ausente, Get tipado sin red, digest skip con force y
// body idéntico, y fail-soft con fetch fallido y cache presente. check
// verifica el valor tipado que devuelve Get().
func runStoreWiring[M any](
	t *testing.T,
	name string,
	raw []byte,
	newStore func(path string, ttl time.Duration, lock bool, opts ...modelscache.Option) *modelscache.Store[M],
	check func(t *testing.T, m M),
) {
	t.Helper()
	ctx := context.Background()
	// hits 1-2 → 200 con raw; hit >= 3 → 404 (no reintenta: oráculo exacto
	// de un solo request por refresh, clase D9 del motor).
	f := newFakeUpstream(t, func(hit int64) (int, []byte) {
		if hit <= 2 {
			return 200, raw
		}
		return 404, []byte(`{"error":"gone"}`)
	})
	path := filepath.Join(t.TempDir(), name+".json")

	store := newStore(path, modelscache.DefaultCacheTTL, true, fakeOpts(f)...)
	// Contrato D8 del Store genérico: campos exportados reflejan el
	// constructor.
	if store.Path != path {
		t.Fatalf("store.Path = %q, want %q", store.Path, path)
	}
	if store.TTL != modelscache.DefaultCacheTTL {
		t.Fatalf("store.TTL = %v, want %v", store.TTL, modelscache.DefaultCacheTTL)
	}
	if !store.Lock {
		t.Fatal("store.Lock = false, want true")
	}

	// 1) Cache ausente → fetch + persist + changed=true (P15).
	changed, err := store.Refresh(ctx, false)
	if err != nil {
		t.Fatalf("Refresh con cache ausente: %v", err)
	}
	if !changed {
		t.Fatal("changed = false en primer write exitoso, want true")
	}
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1", n)
	}
	if !bytes.Equal(readBytes(t, path), raw) {
		t.Fatal("cache no persistió el body servido byte-idéntico")
	}
	if _, statErr := os.Stat(path + ".sha256"); statErr != nil {
		t.Fatalf("sidecar no creado en el primer write: %v", statErr)
	}

	// 2) Get tipado, read-only y sin red (I5 de 001).
	m, err := store.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	check(t, m)
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("Get emitió requests: hits = %d, want 1", n)
	}

	// 3) Digest skip (I4 de 001): force con body idéntico → changed=false
	// sin reescritura (oráculo mtime).
	beforeInfo, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat cache pre-Refresh: %v", statErr)
	}
	changed, err = store.Refresh(ctx, true)
	if err != nil {
		t.Fatalf("Refresh force con body idéntico: %v", err)
	}
	if changed {
		t.Fatal("changed = true con body byte-idéntico, want false (digest skip)")
	}
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("hits = %d, want 2 (force fetcheó una vez)", n)
	}
	afterInfo, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat cache post-Refresh: %v", statErr)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("mtime del cache cambió con body idéntico (%v → %v): reescritura, viola I4",
			beforeInfo.ModTime(), afterInfo.ModTime())
	}

	// 4) Fail-soft (P15/D4): fetch fallido con cache presente → (false, nil).
	ageCache(t, path, modelscache.DefaultCacheTTL+2*time.Minute)
	changed, err = store.Refresh(ctx, false)
	if err != nil {
		t.Fatalf("Refresh con fetch fallido y cache presente: %v, want fail-soft (nil)", err)
	}
	if changed {
		t.Fatal("changed = true con fetch fallido, want false")
	}
	if n := f.hits.Load(); n != 3 {
		t.Fatalf("hits = %d, want 3 (404 no reintenta)", n)
	}
	if !bytes.Equal(readBytes(t, path), raw) {
		t.Fatal("el fetch fallido modificó el cache (viola I6 de 001)")
	}
}

func TestZenStore_Wiring(t *testing.T) {
	runStoreWiring(t, "zen-models", []byte(fixtureZen), NewZenStore, func(t *testing.T, m ModelList) {
		t.Helper()
		if m.Object != "list" || len(m.Models) != 2 || m.Models[0].ID != "qwen3-coder" || m.Models[0].OwnedBy != "opencode" {
			t.Fatalf("Get tipado = %+v, want lista zen fiel", m)
		}
	})
}

func TestGoStore_Wiring(t *testing.T) {
	runStoreWiring(t, "go-models", []byte(fixtureGo), NewGoStore, func(t *testing.T, m ModelList) {
		t.Helper()
		if m.Object != "list" || len(m.Models) != 2 || m.Models[0].ID != "minimax-m3" || m.Models[0].OwnedBy != "opencode" {
			t.Fatalf("Get tipado = %+v, want lista go fiel", m)
		}
	})
}

func TestOpenRouterStore_Wiring(t *testing.T) {
	runStoreWiring(t, "openrouter-models", []byte(fixtureOpenRouter), NewOpenRouterStore, func(t *testing.T, cat OpenRouterCatalog) {
		t.Helper()
		if len(cat.Models) != 2 {
			t.Fatalf("models = %d, want 2", len(cat.Models))
		}
		if cat.Models[0].ID != "deepseek/deepseek-v4-flash" {
			t.Fatalf("Models[0].ID = %q, want deepseek/deepseek-v4-flash", cat.Models[0].ID)
		}
		if p := cat.Models[0].Pricing; p.Prompt != 0.000001 || p.Completion != 0.000004 {
			t.Fatalf("Pricing = %v/%v, want 0.000001/0.000004", p.Prompt, p.Completion)
		}
	})
}
