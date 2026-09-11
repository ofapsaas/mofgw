// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package modelsdev

// Tests de 019-001-fetch-modelsdev (spec: docs/specs/019-001-fetch-modelsdev/
// spec.md, audit: test-audit.md, bloques B1-B11, postcondiciones P1-P17).
//
// Este archivo usa `package modelsdev` (no modelsdev_test) a propósito: el
// RED de esta feature es POR COMPILACIÓN y solo un test file del propio
// paquete produce los `undefined: modelsdev.*` esperados con el paquete
// ausente; un test file externo falla antes con "no non-test Go files", que
// no distingue símbolos. Coincide con la convención del repo
// (provider_test.go es package provider) y con los precedentes 011-005/015-001.
//
// Contrato de campos que estos tests congelan (P15; el resto del shape lo
// define el implementer en GREEN respetando P14/P15):
//
//	Provider.Name / .API / .NPM string
//	Provider.Models map[string]Model  (key = id del modelo, P15)
//	Model.Name string
//	Model.Attachment / .Reasoning / .ToolCall bool
//	Model.Modalities.Input / .Output []string
//	Model.Limit.Context / .Output / .Input entero
//	Model.Cost.Input / .Output / .CacheRead / .CacheWrite float
//
// Semántica de valores (P14): campo ausente → zero-value en la API expuesta
// (NO pointer nil); campo desconocido → ignorado sin error. El parseo
// tolerante puede usar pointers INTERNOS, pero lo que exponen los structs
// son valores.

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
	"syscall"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/build"
)

// fixtureTolerante replica el schema variable real de models.dev (D1/P14):
// un provider completo, uno con campos ausentes y extras (tiers, input_audio,
// context_over_200k, campo desconocido) y uno sin models. Inline y estable
// byte a byte: los tests de digest (B5) dependen de su estabilidad.
const fixtureTolerante = `{
  "anthropic": {
    "id": "anthropic",
    "name": "Anthropic",
    "api": "https://api.anthropic.com/v1",
    "npm": "@ai-sdk/anthropic",
    "doc": "https://docs.anthropic.com",
    "env": ["ANTHROPIC_API_KEY"],
    "models": {
      "claude-sonnet-4-5": {
        "id": "claude-sonnet-4-5",
        "name": "Claude Sonnet 4.5",
        "attachment": true,
        "reasoning": true,
        "reasoning_options": ["enabled"],
        "tool_call": true,
        "modalities": {"input": ["text", "image"], "output": ["text"]},
        "limit": {"context": 200000, "output": 64000, "input": 180000},
        "cost": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75}
      }
    }
  },
  "fakecorp": {
    "id": "fakecorp",
    "name": "FakeCorp",
    "api": "https://api.fakecorp.example/v1",
    "npm": "@ai-sdk/fakecorp",
    "env": ["FAKECORP_API_KEY"],
    "models": {
      "fake-mini": {
        "id": "fake-mini",
        "name": "Fake Mini",
        "reasoning": false,
        "reasoning_options": [],
        "tool_call": true,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 128000, "output": 4096},
        "cost": {"input": 0.15, "output": 0.6, "cache_read": 0.01, "tiers": [{"above": 200000, "input": 0.3}], "input_audio": 10, "context_over_200k": true},
        "campo_futuro_desconocido": {"nested": [1, 2, 3]}
      }
    }
  },
  "sin-models": {
    "id": "sin-models",
    "name": "Sin Models",
    "api": "https://sinmodels.example"
  }
}`

var fixtureBytes = []byte(fixtureTolerante)

// modifiedFixtureBytes es el fixture con un cambio semántico (nombre del
// modelo): cuerpo DISTINTO al cacheado para los tests de re-fetch/force.
var modifiedFixtureBytes = []byte(
	strings.Replace(fixtureTolerante, `"Claude Sonnet 4.5"`, `"Claude Sonnet 4.5 (actualizado)"`, 1))

// fakeUpstream es un upstream fake que responde según respFn(hit) y registra
// los hits (oráculo "cuántos requests hizo el paquete": P5/P7/P8) y los
// User-Agent recibidos (P3). El contador es atómico porque el handler corre
// en la goroutine del server y los asserts en la del test.
type fakeUpstream struct {
	srv  *httptest.Server
	hits atomic.Int64
	mu   sync.Mutex
	uas  []string
}

func newFakeUpstream(t *testing.T, respFn func(hit int64) (status int, body []byte)) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.hits.Add(1)
		f.mu.Lock()
		f.uas = append(f.uas, r.Header.Get("User-Agent"))
		f.mu.Unlock()
		status, body := respFn(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// userAgents devuelve una copia de los User-Agent recibidos.
func (f *fakeUpstream) userAgents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.uas...)
}

// constResp devuelve una respFn que responde siempre igual.
func constResp(status int, body []byte) func(int64) (int, []byte) {
	return func(int64) (int, []byte) { return status, body }
}

// newTestStore arma un Store apuntando al fake. R5/R7 del audit: en tests de
// red se inyecta WithClient(ts.Client()) para no depender del proxy/entorno.
func newTestStore(t *testing.T, path string, ttl time.Duration, lock bool, f *fakeUpstream, extra ...Option) *Store {
	t.Helper()
	opts := []Option{WithBaseURL(f.srv.URL), WithClient(f.srv.Client())}
	opts = append(opts, extra...)
	return NewStore(path, ttl, lock, opts...)
}

func writeCacheFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write cache fixture: %v", err)
	}
}

// writeSidecar pre-crea el sidecar de digest (D6: <cache-path>.sha256, hex
// 64 chars) para el test de skip por body idéntico.
func writeSidecar(t *testing.T, path string, body []byte) {
	t.Helper()
	sum := sha256.Sum256(body)
	if err := os.WriteFile(path+".sha256", []byte(hex.EncodeToString(sum[:])), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

// ageCache envejece el mtime del cache SIN sleeps (R2): os.Chtimes con margen
// amplio respecto del TTL.
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

// holdFlock toma un flock exclusivo no bloqueante sobre <path>.lock en el
// MISMO proceso (R3: determinístico, sin procesos auxiliares). El lock se
// libera al cerrar el fd (cleanup del test).
func holdFlock(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock: %v", err)
	}
}

// --- B1: P1, P2 — Get sirve el cache sin red ---------------------------------

func TestGet_ServesCacheWithoutNetwork(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, fixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes)
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	cat, err := store.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cat == nil || cat.Providers == nil {
		t.Fatal("Get devolvió catálogo nil")
	}
	p, ok := cat.Providers["anthropic"]
	if !ok {
		t.Fatalf("provider anthropic ausente (providers=%d)", len(cat.Providers))
	}
	m, ok := p.Models["claude-sonnet-4-5"]
	if !ok {
		t.Fatalf("modelo claude-sonnet-4-5 ausente (models=%d)", len(p.Models))
	}
	// Datos del fixture por clave (P1): cost/limit/modalities correctos.
	if m.Cost.Input != 3 || m.Cost.Output != 15 || m.Cost.CacheRead != 0.3 || m.Cost.CacheWrite != 3.75 {
		t.Fatalf("cost = %v/%v/%v/%v, want 3/15/0.3/3.75", m.Cost.Input, m.Cost.Output, m.Cost.CacheRead, m.Cost.CacheWrite)
	}
	if m.Limit.Context != 200000 || m.Limit.Output != 64000 || m.Limit.Input != 180000 {
		t.Fatalf("limit = %v/%v/%v, want 200000/64000/180000", m.Limit.Context, m.Limit.Output, m.Limit.Input)
	}
	if len(m.Modalities.Input) != 2 || m.Modalities.Input[0] != "text" || m.Modalities.Input[1] != "image" {
		t.Fatalf("modalities.input = %v, want [text image]", m.Modalities.Input)
	}
	if len(m.Modalities.Output) != 1 || m.Modalities.Output[0] != "text" {
		t.Fatalf("modalities.output = %v, want [text]", m.Modalities.Output)
	}
	// Oráculo hits==0: Get jamás hace red (I5/P1).
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("Get emitió %d requests, want 0", n)
	}
}

func TestGet_ServesStaleCache(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes)
	// Vencido hace TTL+2min: la frescura no bloquea la lectura (P2); la
	// semántica stale es decisión del llamador. Sin sleeps: os.Chtimes (R2).
	ageCache(t, path, DefaultCacheTTL+2*time.Minute)
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	cat, err := store.Get()
	if err != nil {
		t.Fatalf("Get con cache stale: %v", err)
	}
	if _, ok := cat.Providers["anthropic"]; !ok {
		t.Fatal("cache stale no servido")
	}
	// Sirve el stale, no el upstream (que ofrecería el body modificado).
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("Get stale emitió %d requests, want 0", n)
	}
}

// --- B2: P3 — Fetch devuelve catálogo + raw + UA ------------------------------

func TestFetch_CatalogRawAndUserAgent(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, fixtureBytes))

	cat, raw, err := Fetch(context.Background(), WithBaseURL(f.srv.URL), WithClient(f.srv.Client()))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if cat == nil || cat.Providers == nil {
		t.Fatal("Fetch devolvió catálogo nil")
	}
	// raw byte-idéntico al body servido (P3): el cache queda fiel al upstream.
	if !bytes.Equal(raw, fixtureBytes) {
		t.Fatalf("raw no es byte-idéntico al body servido (len raw=%d, want %d)", len(raw), len(fixtureBytes))
	}
	// User-Agent propio (P3/I7): mofgw/<version>, nunca el default de Go.
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
	// Catálogo tipado fiel del body fake (P15, subset).
	if m := cat.Providers["anthropic"].Models["claude-sonnet-4-5"]; m.Cost.Input != 3 {
		t.Fatalf("cost.input = %v, want 3", m.Cost.Input)
	}
}

// --- B3: P4 — timeout + retry transitorio -------------------------------------

func TestFetch_Timeout(t *testing.T) {
	// Sanity del contrato: el presupuesto del test (200ms) es menor al tope
	// por intento, así el deadline del LLAMADOR es el que corta (decisión P4
	// del audit: ctx 200ms, sin inyectar client Timeout).
	if FetchTimeout <= 200*time.Millisecond {
		t.Fatalf("FetchTimeout = %v, want > 200ms para que el ctx del test corte primero", FetchTimeout)
	}
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-r.Context().Done() // bloquea hasta que el cliente corta
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := Fetch(ctx, WithBaseURL(srv.URL), WithClient(srv.Client()))
	if err == nil {
		t.Fatal("timeout no surfaced como error")
	}
	// Un intento colgado ya consumió el presupuesto: sin retry por timeout (P4).
	if n := hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1 (el timeout no reintenta)", n)
	}
}

func TestFetch_RetryTransient(t *testing.T) {
	f := newFakeUpstream(t, func(hit int64) (int, []byte) {
		if hit == 1 {
			return 500, []byte(`{"error":"boom"}`)
		}
		return 200, fixtureBytes
	})

	cat, _, err := Fetch(context.Background(), WithBaseURL(f.srv.URL), WithClient(f.srv.Client()))
	if err != nil {
		t.Fatalf("Fetch con 500→200: %v (el 500 es transitorio y debe reintentar)", err)
	}
	if cat == nil {
		t.Fatal("catálogo nil tras retry exitoso")
	}
	// 2 intentos totales (D9): 1 reintento con backoff ~500ms (R1: aceptado).
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("hits = %d, want 2 (500 transitorio → 1 reintento)", n)
	}
}

func TestFetch_NoRetryOn4xx(t *testing.T) {
	f := newFakeUpstream(t, constResp(404, []byte("not found")))

	_, _, err := Fetch(context.Background(), WithBaseURL(f.srv.URL), WithClient(f.srv.Client()))
	if err == nil {
		t.Fatal("404 no surfaced como error")
	}
	// 4xx NO es transitorio: error inmediato sin reintento (P4/D9).
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1 (4xx no reintenta)", n)
	}
}

// --- B4: P5-P7 — TTL y force ---------------------------------------------------

func TestRefresh_FreshSkips(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes) // mtime recién escrito: fresco
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	// Contrato D3: los campos exportados de Store reflejan el constructor.
	if store.Path != path {
		t.Fatalf("store.Path = %q, want %q", store.Path, path)
	}
	if store.TTL != DefaultCacheTTL {
		t.Fatalf("store.TTL = %v, want %v", store.TTL, DefaultCacheTTL)
	}
	if store.Lock {
		t.Fatal("store.Lock = true, want false")
	}

	changed, err := store.Refresh(context.Background(), false)
	if err != nil {
		t.Fatalf("Refresh con cache fresco: %v", err)
	}
	if changed {
		t.Fatal("changed = true con cache fresco, want false")
	}
	// Oráculo hits==0: cache dentro de TTL → NO fetch (P5).
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("hits = %d, want 0 (cache fresco no refreshea)", n)
	}
	// Cache byte-intacto.
	if !bytes.Equal(readBytes(t, path), fixtureBytes) {
		t.Fatal("cache fresco fue modificado por Refresh")
	}
}

func TestRefresh_StaleRefetches(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes)
	viejo := time.Now().Add(-(DefaultCacheTTL + 2*time.Minute))
	ageCache(t, path, DefaultCacheTTL+2*time.Minute)
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	changed, err := store.Refresh(context.Background(), false)
	if err != nil {
		t.Fatalf("Refresh con cache stale: %v", err)
	}
	if !changed {
		t.Fatal("changed = false con cache stale y body nuevo, want true")
	}
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1 (stale → re-fetch)", n)
	}
	// Cache y sidecar reescritos con el body nuevo (P6).
	if !bytes.Equal(readBytes(t, path), modifiedFixtureBytes) {
		t.Fatal("cache no fue reescrito con el body nuevo")
	}
	sum := sha256.Sum256(modifiedFixtureBytes)
	side := readBytes(t, path+".sha256")
	if strings.TrimSpace(string(side)) != hex.EncodeToString(sum[:]) {
		t.Fatalf("sidecar = %q, want digest del body nuevo", side)
	}
	// Nuevo mtime (P6): posterior al timestamp viejo que impusimos.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if !info.ModTime().After(viejo) {
		t.Fatalf("mtime = %v, want > %v (reescritura debe renovar mtime)", info.ModTime(), viejo)
	}
}

func TestRefresh_Force(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes) // fresco, pero force=true igual fetchea
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	changed, err := store.Refresh(context.Background(), true)
	if err != nil {
		t.Fatalf("Refresh force: %v", err)
	}
	// force=true fetchea aunque el cache esté fresco (P7)...
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1 (force fuerza el fetch)", n)
	}
	// ...y con body distinto reescribe (P7).
	if !changed {
		t.Fatal("changed = false con force y body distinto, want true")
	}
	if !bytes.Equal(readBytes(t, path), modifiedFixtureBytes) {
		t.Fatal("cache no fue reescrito con force")
	}
}

// --- B5: P8 — digest skip -------------------------------------------------------

func TestRefresh_SkipsIdenticalBody(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, fixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes)
	writeSidecar(t, path, fixtureBytes)
	beforeCache := readBytes(t, path)
	beforeSide := readBytes(t, path+".sha256")
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	// force=true garantiza que HAYA fetch aunque el mtime esté fresco: así el
	// skip proviene del digest (P8/I4), no del TTL.
	changed, err := store.Refresh(context.Background(), true)
	if err != nil {
		t.Fatalf("Refresh con body idéntico: %v", err)
	}
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1 (force debe haber fetcheado)", n)
	}
	if changed {
		t.Fatal("changed = true con body byte-idéntico, want false (D6)")
	}
	// Ni cache ni sidecar reescritos (P8): byte-idénticos a antes.
	if !bytes.Equal(readBytes(t, path), beforeCache) {
		t.Fatal("cache fue reescrito con body idéntico (viola I4)")
	}
	if !bytes.Equal(readBytes(t, path+".sha256"), beforeSide) {
		t.Fatal("sidecar fue reescrito con body idéntico (viola I4)")
	}
}

// --- B6: P9, P10 — lock exclusivo no bloqueante ---------------------------------

func TestRefresh_LockBusyFailsFast(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes)
	// Stale: si el Refresh pasara el lock, fetchearía (hits>1); el oráculo
	// hits==0 prueba que no llegó a fetchear. También fuerza que el lock se
	// tome ANTES del chequeo de TTL: dos procesos que vieran stale a la vez
	// fetchearían ambos — exactamente la carrera que el lock existe para
	// evitar, así que adquirir el lock es lo primero.
	ageCache(t, path, DefaultCacheTTL+2*time.Minute)
	holdFlock(t, path) // R3: flock sostenido en el mismo proceso
	store := newTestStore(t, path, DefaultCacheTTL, true, f)

	start := time.Now()
	changed, err := store.Refresh(context.Background(), false)
	elapsed := time.Since(start)

	// ErrLockBusy comparable con errors.Is (wrapping ok).
	if err == nil || !errors.Is(err, ErrLockBusy) {
		t.Fatalf("err = %v, want ErrLockBusy (via errors.Is)", err)
	}
	if changed {
		t.Fatal("changed = true con lock busy, want false")
	}
	// Falla RÁPIDA (P9): LOCK_NB no espera. Guard anti-regresión a lock
	// bloqueante; el bound es generoso para no flakear.
	if elapsed > 5*time.Second {
		t.Fatalf("Refresh tardó %v con lock busy, want falla rápida", elapsed)
	}
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("hits = %d, want 0 (lock busy → no fetch)", n)
	}
	// Cache byte-intacto (I6).
	if !bytes.Equal(readBytes(t, path), fixtureBytes) {
		t.Fatal("cache fue modificado bajo lock busy")
	}
}

func TestRefresh_NoLockMode(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes)
	ageCache(t, path, DefaultCacheTTL+2*time.Minute)
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	changed, err := store.Refresh(context.Background(), false)
	if err != nil {
		t.Fatalf("Refresh con Lock=false: %v", err)
	}
	if !changed {
		t.Fatal("changed = false con cache stale y Lock=false, want true")
	}
	if n := f.hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1", n)
	}
	// P10: Lock=false no crea ni adquiere lock file.
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatal("Lock=false creó un .lock file")
	}
}

// --- B7: P11, P12 — fail-soft / fail-loud ----------------------------------------

func TestRefresh_FailSoftWithCache(t *testing.T) {
	f := newFakeUpstream(t, constResp(500, []byte(`{"error":"boom"}`)))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	writeCacheFile(t, path, fixtureBytes)
	ageCache(t, path, DefaultCacheTTL+2*time.Minute) // stale → intentaría fetch
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	changed, err := store.Refresh(context.Background(), false)
	// D4: fetch fallido con cache presente → se sirve el stale, error NO
	// surfaced (fail-soft).
	if err != nil {
		t.Fatalf("Refresh con fetch fallido y cache presente devolvió error %v, want fail-soft (nil)", err)
	}
	if changed {
		t.Fatal("changed = true con fetch fallido, want false")
	}
	// 500 es transitorio: 2 intentos totales (D9).
	if n := f.hits.Load(); n != 2 {
		t.Fatalf("hits = %d, want 2 (500 → 1 reintento)", n)
	}
	// Cache byte-intacto (I6) y Get() sigue sirviendo el stale (P11).
	if !bytes.Equal(readBytes(t, path), fixtureBytes) {
		t.Fatal("cache fue modificado por un fetch fallido")
	}
	cat, err := store.Get()
	if err != nil {
		t.Fatalf("Get tras fetch fallido: %v", err)
	}
	if _, ok := cat.Providers["anthropic"]; !ok {
		t.Fatal("Get no sirve el cache stale tras fetch fallido")
	}
}

func TestRefresh_FailLoudWithoutCache(t *testing.T) {
	f := newFakeUpstream(t, constResp(500, []byte(`{"error":"boom"}`)))
	path := filepath.Join(t.TempDir(), "models-dev.json")
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	changed, err := store.Refresh(context.Background(), false)
	// Sin cache Y sin fetch posible → error surfaced (fail-loud, D4/P12).
	if err == nil {
		t.Fatal("Refresh sin cache y con fetch fallido devolvió nil, want error")
	}
	if changed {
		t.Fatal("changed = true sin cache y con fetch fallido, want false")
	}
	// No se crea ningún archivo de cache ni sidecar (P12).
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("se creó un cache file con fetch fallido y sin cache previo")
	}
	if _, err := os.Stat(path + ".sha256"); !os.IsNotExist(err) {
		t.Fatal("se creó un sidecar con fetch fallido y sin cache previo")
	}
}

// --- B8: P13 — knob de deshabilitación -------------------------------------------

func TestFetchDisabled_Knob(t *testing.T) {
	const knob = "MOFGW_DISABLE_MODELS_FETCH"
	ctx := context.Background()

	t.Run("fetch_devuelve_ErrFetchDisabled_sin_request", func(t *testing.T) {
		t.Setenv(knob, "1") // R4: t.Setenv por test, sin t.Parallel
		f := newFakeUpstream(t, constResp(200, fixtureBytes))
		_, _, err := Fetch(ctx, WithBaseURL(f.srv.URL), WithClient(f.srv.Client()))
		// D5: el knob devuelve el error TIPADO, no un fetch silenciosamente
		// omitido; comparable con errors.Is.
		if err == nil || !errors.Is(err, ErrFetchDisabled) {
			t.Fatalf("err = %v, want ErrFetchDisabled (via errors.Is)", err)
		}
		// Sin NINGÚN request (D5/P13).
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("hits = %d, want 0 (knob activo no emite requests)", n)
		}
	})

	t.Run("refresh_con_cache_sirve_sin_fetch", func(t *testing.T) {
		t.Setenv(knob, "1")
		f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes))
		path := filepath.Join(t.TempDir(), "models-dev.json")
		writeCacheFile(t, path, fixtureBytes)
		store := newTestStore(t, path, DefaultCacheTTL, false, f)
		// force=true incluso: con knob activo y cache presente, el cache se
		// sirve y no hay fetch (D5: fail-soft).
		changed, err := store.Refresh(ctx, true)
		if err != nil {
			t.Fatalf("Refresh con knob y cache presente: %v, want (false, nil)", err)
		}
		if changed {
			t.Fatal("changed = true con knob activo, want false")
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("hits = %d, want 0", n)
		}
	})

	t.Run("refresh_sin_cache_falla_con_ErrFetchDisabled", func(t *testing.T) {
		t.Setenv(knob, "1")
		f := newFakeUpstream(t, constResp(200, fixtureBytes))
		path := filepath.Join(t.TempDir(), "models-dev.json")
		store := newTestStore(t, path, DefaultCacheTTL, false, f)
		changed, err := store.Refresh(ctx, false)
		if err == nil || !errors.Is(err, ErrFetchDisabled) {
			t.Fatalf("err = %v, want ErrFetchDisabled (sin cache y knob activo)", err)
		}
		if changed {
			t.Fatal("changed = true con knob activo y sin cache, want false")
		}
		if n := f.hits.Load(); n != 0 {
			t.Fatalf("hits = %d, want 0", n)
		}
	})

	t.Run("knob_en_0_es_comportamiento_normal", func(t *testing.T) {
		t.Setenv(knob, "0") // P13/I8: vacía o 0 → fetch-on
		f := newFakeUpstream(t, constResp(200, fixtureBytes))
		cat, _, err := Fetch(ctx, WithBaseURL(f.srv.URL), WithClient(f.srv.Client()))
		if err != nil {
			t.Fatalf("Fetch con knob=0: %v, want comportamiento normal", err)
		}
		if cat == nil {
			t.Fatal("catálogo nil con knob=0")
		}
		if n := f.hits.Load(); n != 1 {
			t.Fatalf("hits = %d, want 1 (knob=0 fetchea)", n)
		}
	})
}

// --- B9: P14, P15 — parseo tolerante y fiel --------------------------------------

func TestParseCatalog_TolerantShape(t *testing.T) {
	cat, err := ParseCatalog(fixtureBytes)
	// El fixture con schema variable (tiers, input_audio, context_over_200k,
	// reasoning_options vacío, campo desconocido) parsea sin error (P14/I9).
	if err != nil {
		t.Fatalf("ParseCatalog de fixture tolerante: %v", err)
	}
	if cat == nil || cat.Providers == nil {
		t.Fatal("catálogo nil")
	}
	if len(cat.Providers) != 3 {
		t.Fatalf("providers = %d, want 3", len(cat.Providers))
	}
	// Provider sin models → válido, mapa vacío (P14).
	sm, ok := cat.Providers["sin-models"]
	if !ok {
		t.Fatal("provider sin-models ausente")
	}
	if len(sm.Models) != 0 {
		t.Fatalf("models de provider sin models = %d, want 0", len(sm.Models))
	}
	m := cat.Providers["fakecorp"].Models["fake-mini"]
	// Campos ausentes → zero-value (P14): limit.input y cost.cache_write.
	if m.Limit.Input != 0 {
		t.Fatalf("limit.input ausente = %v, want 0", m.Limit.Input)
	}
	if m.Cost.CacheWrite != 0 {
		t.Fatalf("cost.cache_write ausente = %v, want 0", m.Cost.CacheWrite)
	}
	// attachment ausente → false (zero-value de un campo P15).
	if m.Attachment {
		t.Fatal("attachment ausente = true, want false (zero-value)")
	}
	// Los campos presentes se parsean igual aunque haya extras en el objeto.
	if m.Cost.Input != 0.15 || m.Cost.Output != 0.6 || m.Cost.CacheRead != 0.01 {
		t.Fatalf("cost = %v/%v/%v, want 0.15/0.6/0.01", m.Cost.Input, m.Cost.Output, m.Cost.CacheRead)
	}
	if m.Limit.Context != 128000 || m.Limit.Output != 4096 {
		t.Fatalf("limit = %v/%v, want 128000/4096", m.Limit.Context, m.Limit.Output)
	}
}

func TestParseCatalog_FaithfulFields(t *testing.T) {
	cat, err := ParseCatalog(fixtureBytes)
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	// Key del mapa = id del provider (P15).
	p, ok := cat.Providers["anthropic"]
	if !ok {
		t.Fatal("provider anthropic no está keyed por id")
	}
	if p.Name != "Anthropic" {
		t.Fatalf("name = %q, want Anthropic", p.Name)
	}
	if p.API != "https://api.anthropic.com/v1" {
		t.Fatalf("api = %q, want https://api.anthropic.com/v1", p.API)
	}
	if p.NPM != "@ai-sdk/anthropic" {
		t.Fatalf("npm = %q, want @ai-sdk/anthropic", p.NPM)
	}
	// Key del mapa = id del modelo (P15); campos expuestos tal cual (I2:
	// sin normalización semántica).
	m, ok := p.Models["claude-sonnet-4-5"]
	if !ok {
		t.Fatal("modelo claude-sonnet-4-5 no está keyed por id")
	}
	if m.Name != "Claude Sonnet 4.5" {
		t.Fatalf("name = %q, want Claude Sonnet 4.5", m.Name)
	}
	if !m.ToolCall || !m.Reasoning || !m.Attachment {
		t.Fatalf("tool_call/reasoning/attachment = %v/%v/%v, want true/true/true", m.ToolCall, m.Reasoning, m.Attachment)
	}
	if m.Cost.Input != 3 || m.Cost.Output != 15 || m.Cost.CacheRead != 0.3 || m.Cost.CacheWrite != 3.75 {
		t.Fatalf("cost = %v/%v/%v/%v, want 3/15/0.3/3.75", m.Cost.Input, m.Cost.Output, m.Cost.CacheRead, m.Cost.CacheWrite)
	}
	if m.Limit.Context != 200000 || m.Limit.Output != 64000 || m.Limit.Input != 180000 {
		t.Fatalf("limit = %v/%v/%v, want 200000/64000/180000", m.Limit.Context, m.Limit.Output, m.Limit.Input)
	}
	if len(m.Modalities.Input) != 2 || m.Modalities.Input[0] != "text" || m.Modalities.Input[1] != "image" {
		t.Fatalf("modalities.input = %v, want [text image]", m.Modalities.Input)
	}
	if len(m.Modalities.Output) != 1 || m.Modalities.Output[0] != "text" {
		t.Fatalf("modalities.output = %v, want [text]", m.Modalities.Output)
	}
}

// --- B10: P16 — escritura atómica y directory-creation ----------------------------

func TestRefresh_AtomicWrite(t *testing.T) {
	f := newFakeUpstream(t, constResp(200, fixtureBytes))
	dir := t.TempDir()
	// Path en subdirectorio anidado que NO existe: Refresh debe crearlo (P16).
	path := filepath.Join(dir, "nested", "deeper", "models-dev.json")
	store := newTestStore(t, path, DefaultCacheTTL, false, f)

	changed, err := store.Refresh(context.Background(), false)
	if err != nil {
		t.Fatalf("Refresh a subdirectorio inexistente: %v", err)
	}
	// Primer write exitoso sin sidecar previo → changed=true (D6).
	if !changed {
		t.Fatal("changed = false en primer write exitoso, want true")
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("el directorio del cache no fue creado: %v", err)
	}
	// El archivo final es JSON parseable completo, nunca truncado (P16/I3).
	raw := readBytes(t, path)
	if !json.Valid(raw) {
		t.Fatalf("cache final no es JSON válido (escritura no atómica?): %.100s", raw)
	}
	if !bytes.Equal(raw, fixtureBytes) {
		t.Fatal("cache final no es byte-idéntico al body upstream")
	}
	// Sidecar escrito en el primer write (D6).
	if _, err := os.Stat(path + ".sha256"); err != nil {
		t.Fatalf("sidecar no creado en primer write: %v", err)
	}
	// Sin .tmp-* huérfanos (patrón persist_test:242).
	resid, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp-*"))
	if err != nil {
		t.Fatalf("glob tmp: %v", err)
	}
	if len(resid) != 0 {
		t.Fatalf("quedaron .tmp-* residuales: %v", resid)
	}
}

// --- B11: P17 — logs estructurados -------------------------------------------------

// captureLogs devuelve un buffer + logger slog JSON para asertar eventos (P17).
func captureLogs() (*bytes.Buffer, *slog.Logger) {
	buf := &bytes.Buffer{}
	return buf, slog.New(slog.NewJSONHandler(buf, nil))
}

// logRecords decodifica cada línea del buffer a un record JSON.
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

// findEvent busca el record cuyo msg sea el evento pedido (D7: el nombre del
// evento va en el mensaje del record slog).
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

func assertFields(t *testing.T, rec map[string]any, event string, fields ...string) {
	t.Helper()
	for _, k := range fields {
		if _, ok := rec[k]; !ok {
			t.Fatalf("evento %q sin campo %q: %v", event, k, rec)
		}
	}
}

func TestLogEvents(t *testing.T) {
	ctx := context.Background()

	t.Run("fetch_ok", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, fixtureBytes))
		if _, _, err := Fetch(ctx, WithBaseURL(f.srv.URL), WithClient(f.srv.Client()), WithLogger(logger)); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		rec := findEvent(t, logRecords(t, buf), "fetch_ok")
		assertFields(t, rec, "fetch_ok", "bytes", "duration")
	})

	t.Run("fetch_failed", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(500, []byte(`{"error":"boom"}`)))
		_, _, _ = Fetch(ctx, WithBaseURL(f.srv.URL), WithClient(f.srv.Client()), WithLogger(logger)) // el error es esperado
		rec := findEvent(t, logRecords(t, buf), "fetch_failed")
		assertFields(t, rec, "fetch_failed", "attempt", "err")
	})

	t.Run("cache_hit", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes)) // no debe pegarle
		path := filepath.Join(t.TempDir(), "models-dev.json")
		writeCacheFile(t, path, fixtureBytes) // fresco → stale=false
		store := newTestStore(t, path, DefaultCacheTTL, false, f, WithLogger(logger))
		if _, err := store.Get(); err != nil {
			t.Fatalf("Get: %v", err)
		}
		rec := findEvent(t, logRecords(t, buf), "cache_hit")
		assertFields(t, rec, "cache_hit", "stale")
		if v, ok := rec["stale"].(bool); !ok || v {
			t.Fatalf("cache_hit.stale = %v, want false (cache fresco)", rec["stale"])
		}
	})

	t.Run("skipped_identical", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, fixtureBytes))
		path := filepath.Join(t.TempDir(), "models-dev.json")
		writeCacheFile(t, path, fixtureBytes)
		writeSidecar(t, path, fixtureBytes)
		store := newTestStore(t, path, DefaultCacheTTL, false, f, WithLogger(logger))
		// force=true: el skip proviene del digest, no del TTL.
		if changed, err := store.Refresh(ctx, true); err != nil || changed {
			t.Fatalf("Refresh idéntico = (%v, %v), want (false, nil)", changed, err)
		}
		rec := findEvent(t, logRecords(t, buf), "skipped_identical")
		assertFields(t, rec, "skipped_identical", "sha256")
		// D6: digest hex de 64 chars.
		if s, ok := rec["sha256"].(string); !ok || len(s) != 64 {
			t.Fatalf("skipped_identical.sha256 = %v, want hex de 64 chars", rec["sha256"])
		}
	})

	t.Run("lock_busy", func(t *testing.T) {
		buf, logger := captureLogs()
		f := newFakeUpstream(t, constResp(200, modifiedFixtureBytes)) // no debe pegarle
		path := filepath.Join(t.TempDir(), "models-dev.json")
		writeCacheFile(t, path, fixtureBytes)
		ageCache(t, path, DefaultCacheTTL+2*time.Minute)
		holdFlock(t, path)
		store := newTestStore(t, path, DefaultCacheTTL, true, f, WithLogger(logger))
		if _, err := store.Refresh(ctx, false); err == nil || !errors.Is(err, ErrLockBusy) {
			t.Fatalf("err = %v, want ErrLockBusy", err)
		}
		rec := findEvent(t, logRecords(t, buf), "lock_busy")
		assertFields(t, rec, "lock_busy", "path")
		if p, ok := rec["path"].(string); !ok || !strings.Contains(p, filepath.Base(path)) {
			t.Fatalf("lock_busy.path = %v, want path que incluya %q", rec["path"], filepath.Base(path))
		}
	})
}
