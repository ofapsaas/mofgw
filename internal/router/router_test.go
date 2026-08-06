// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/health"
	"github.com/ofapsaas/mofgw/internal/provider"
)

// ---- fakes ----

// fakeProvider es un provider programable para tests (sin red).
type fakeProvider struct {
	id        string
	models    []string
	maxTokens int64
	mu        sync.Mutex
	errFor    map[string]error // keyed por "complete" (fallo permanente)
	resp      map[string]*provider.ChatResponse
	calls     map[string]int
	lastBody  []byte // último body recibido (para verificar clamp)
	failN     int    // 002-001: falla los primeros N intentos, luego OK
	transErr  error  // error de los primeros failN intentos (transitorio)
}

func newFake(id string, models ...string) *fakeProvider {
	return &fakeProvider{
		id: id, models: models, maxTokens: 4096,
		errFor: map[string]error{}, resp: map[string]*provider.ChatResponse{}, calls: map[string]int{},
	}
}

func (f *fakeProvider) ID() string     { return f.id }
func (f *fakeProvider) MaxTokens() int { return int(f.maxTokens) }
func (f *fakeProvider) Serves(m string) bool {
	for _, x := range f.models {
		if x == m {
			return true
		}
	}
	return false
}
func (f *fakeProvider) Models() []string { return f.models }

func (f *fakeProvider) fail500() *fakeProvider {
	f.errFor["complete"] = &provider.ErrUpstream{StatusCode: 500, Type: "upstream_error", Message: "boom 500"}
	return f
}
func (f *fakeProvider) fail429() *fakeProvider {
	f.errFor["complete"] = &provider.ErrUpstream{StatusCode: 429, Type: "upstream_error", Message: "rate limited"}
	return f
}
func (f *fakeProvider) fail400() *fakeProvider {
	f.errFor["complete"] = &provider.ErrUpstream{StatusCode: 400, Type: "invalid_request_error", Message: "bad request"}
	return f
}

// failThenOK: falla con el error dado los primeros n intentos, luego
// responde ok("") (002-001: reintento absorbe el fallo transitorio).
// NO toca errFor (fallo permanente): es un modo transitorio aparte.
func (f *fakeProvider) failThenOK(n int, err error) *fakeProvider {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failN = n
	f.transErr = err
	return f
}
func (f *fakeProvider) withMaxTokens(n int64) *fakeProvider { f.maxTokens = n; return f }
func (f *fakeProvider) ok(content string) *fakeProvider {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failN = 0
	delete(f.errFor, "complete") // ok() desactiva fallos previos
	f.resp["complete"] = &provider.ChatResponse{
		ID:    "chatcmpl-" + f.id,
		Model: f.models[0],
		Choices: []provider.Choice{
			{Index: 0, Message: provider.ChatMessage{Role: "assistant", Content: content}, FinishReason: "stop"},
		},
	}
	return f
}

func (f *fakeProvider) Complete(ctx context.Context, body []byte) (*provider.CompleteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["complete"]++
	f.lastBody = body
	if f.failN > 0 {
		f.failN--
		return nil, f.transErr
	}
	if err, ok := f.errFor["complete"]; ok {
		return nil, err
	}
	var req provider.ChatRequest
	_ = json.Unmarshal(body, &req)
	resp := &provider.ChatResponse{ID: "chatcmpl-" + f.id, Model: req.Model}
	if r, ok := f.resp["complete"]; ok {
		resp = r
	}
	raw, _ := json.Marshal(resp)
	return &provider.CompleteResult{Response: resp, Raw: raw}, nil
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls["complete"]
}

// lastBodyClamped: devuelve el max_tokens del último body recibido.
func (f *fakeProvider) lastBodyMaxTokens() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req provider.ChatRequest
	_ = json.Unmarshal(f.lastBody, &req)
	if req.MaxTokens == nil {
		return 0
	}
	return *req.MaxTokens
}

// Stream: stub para satisfacer la interfaz (los tests de streaming usan
// streamFake). Devuelve un único evento con el contenido ok() si está
// configurado; si no, error 500 pre-primer-byte.
func (f *fakeProvider) Stream(ctx context.Context, body []byte) (<-chan provider.StreamEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errFor["complete"]; ok {
		return nil, err
	}
	ch := make(chan provider.StreamEvent, 2)
	if resp, ok := f.resp["complete"]; ok {
		content := ""
		if len(resp.Choices) > 0 {
			content = resp.Choices[0].Message.Content
		}
		ch <- provider.StreamEvent{Data: `{"id":"` + resp.ID + `","choices":[{"delta":{"content":"` + content + `"}}]}`}
	}
	ch <- provider.StreamEvent{Data: "[DONE]"}
	close(ch)
	return ch, nil
}

// streamFake: variante streaming con script de eventos.
type streamFake struct {
	*fakeProvider
	script []provider.StreamEvent // nil script = error pre-primer-byte
}

func (f *streamFake) Stream(ctx context.Context, body []byte) (<-chan provider.StreamEvent, error) {
	f.mu.Lock()
	f.lastBody = body
	f.mu.Unlock()
	if f.script == nil {
		return nil, &provider.ErrUpstream{StatusCode: 500, Type: "upstream_error", Message: "down before first byte"}
	}
	ch := make(chan provider.StreamEvent, len(f.script)+1)
	go func() {
		defer close(ch)
		for _, ev := range f.script {
			ch <- ev
		}
	}()
	return ch, nil
}

// ---- helpers ----

func reqFor(model string) *provider.ChatRequest {
	return &provider.ChatRequest{Model: model, Messages: []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`)}}
}

func bodyFor(model string, maxTokens *int64) []byte {
	b, _ := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": maxTokens,
	})
	return b
}

func durPtr(d time.Duration) *time.Duration { return &d }

// ---- tests ----

func TestFirstProviderOKNoFallback(t *testing.T) {
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)

	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Response.Choices[0].Message.Content != "de p1" {
		t.Fatalf("content = %q, want de p1", res.Response.Choices[0].Message.Content)
	}
	if p1.callCount() != 1 || p2.callCount() != 0 {
		t.Fatalf("calls = p1:%d p2:%d, want 1/0", p1.callCount(), p2.callCount())
	}
}

func TestFailoverToThirdProvider(t *testing.T) {
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").fail500()
	p3 := newFake("p3", "m").ok("de p3")
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}, {Provider: p3}}, 2, time.Minute, 0, 30*time.Second, nil)

	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Response.Choices[0].Message.Content != "de p3" {
		t.Fatalf("content = %q, want de p3", res.Response.Choices[0].Message.Content)
	}
}

func TestCooldownSkipsFailedProvider(t *testing.T) {
	now := time.Now()
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").ok("de p2")
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)
	cs := r.Cooldowns()
	cs.now = func() time.Time { return now }

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err != nil {
		t.Fatalf("primer request: %v", err)
	}
	if !cs.IsCooling("p1") {
		t.Fatal("p1 debería estar en cooldown")
	}
	// p1 "se arregló" pero está en cooldown: el segundo request debe saltearlo
	p1.ok("arreglado")
	before := p1.callCount()
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err != nil {
		t.Fatalf("segundo request: %v", err)
	}
	if p1.callCount() != before {
		t.Fatal("p1 recibió tráfico estando en cooldown")
	}
}

func TestCooldownExpires(t *testing.T) {
	now := time.Now()
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").ok("de p2")
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)
	cs := r.Cooldowns()
	cs.now = func() time.Time { return now }

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err != nil {
		t.Fatalf("primer request: %v", err)
	}
	now = now.Add(2 * time.Minute) // más allá de cooldown
	if cs.IsCooling("p1") {
		t.Fatal("p1 sigue en cooldown tras expirar")
	}
	p1.ok("arreglado")
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err != nil {
		t.Fatalf("request post-expiración: %v", err)
	}
	if p1.callCount() == 0 {
		t.Fatal("p1 no fue probado tras expirar el cooldown")
	}
}

func TestPerProviderCooldownOverride(t *testing.T) {
	now := time.Now()
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").ok("de p2")
	r := New([]ProviderSpec{{Provider: p1, Cooldown: durPtr(90 * time.Second)}, {Provider: p2}}, 2, 60*time.Second, 0, 30*time.Second, nil)
	cs := r.Cooldowns()
	cs.now = func() time.Time { return now }

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err != nil {
		t.Fatalf("primer request: %v", err)
	}
	now = now.Add(70 * time.Second)
	if !cs.IsCooling("p1") {
		t.Fatal("override 90s: p1 debería seguir en cooldown a los 70s (global expiró a los 60s)")
	}
}

func TestClientErrorNoFallback(t *testing.T) {
	p1 := newFake("p1", "m").fail400()
	p2 := newFake("p2", "m").ok("de p2")
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)

	_, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", ce.Status)
	}
	if p2.callCount() != 0 {
		t.Fatal("4xx de cliente no debe probar el siguiente provider")
	}
}

func TestNoProviderServesModel(t *testing.T) {
	p1 := newFake("p1", "m1")
	r := New([]ProviderSpec{{Provider: p1}}, 2, time.Minute, 0, 30*time.Second, nil)

	_, err := r.Complete(context.Background(), reqFor("no-such-model"), bodyFor("no-such-model", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusNotFound || ce.Type != "model_not_found" {
		t.Fatalf("status/type = %d/%s, want 404/model_not_found", ce.Status, ce.Type)
	}
	if p1.callCount() != 0 {
		t.Fatal("no debe haber intentos de red sin provider que sirva el modelo")
	}
}

func TestAllProvidersFail(t *testing.T) {
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").fail429()
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)

	_, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusBadGateway || ce.Type != "upstream_error" {
		t.Fatalf("status/type = %d/%s, want 502/upstream_error", ce.Status, ce.Type)
	}
	if ce.Message == "" || len(ce.Message) > 300 {
		t.Fatalf("mensaje saneado raro: %q", ce.Message)
	}
}

func TestMaxRetriesCapsAttempts(t *testing.T) {
	ps := []*fakeProvider{
		newFake("p1", "m").fail500(), newFake("p2", "m").fail500(), newFake("p3", "m").fail500(),
		newFake("p4", "m").fail500(), newFake("p5", "m").fail500(),
	}
	specs := make([]ProviderSpec, len(ps))
	for i, p := range ps {
		specs[i] = ProviderSpec{Provider: p}
	}
	r := New(specs, 2, time.Minute, 0, 30*time.Second, nil)

	_, _ = r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	total := 0
	for _, p := range ps {
		total += p.callCount()
	}
	if total != 3 {
		t.Fatalf("intentos totales = %d, want 3 (max_retries+1)", total)
	}
}

func TestClampPerProvider(t *testing.T) {
	// p1 sin límite configurado (max_tokens=0 → no clamp); p2 con límite 2048
	p1 := newFake("p1", "m").withMaxTokens(0).ok("sin limite")
	p2 := newFake("p2", "m").withMaxTokens(2048).ok("con limite")
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)

	big := int64(131072)
	// p1 gana: el body no se clampea
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", &big)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if p1.lastBodyMaxTokens() != 131072 {
		t.Fatalf("p1: max_tokens = %d, want 131072 (sin clamp)", p1.lastBodyMaxTokens())
	}

	// ahora p1 falla → p2: el body debe llegar clampado a 2048
	p1.fail500()
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", &big)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if p2.lastBodyMaxTokens() != 2048 {
		t.Fatalf("p2: max_tokens = %d, want 2048 (clampado)", p2.lastBodyMaxTokens())
	}
}

func TestCooldownStoreCleanup(t *testing.T) {
	now := time.Now()
	cs := NewCooldownStore(func() time.Time { return now }, 0)
	cs.Set("a", time.Minute)
	cs.Set("b", 10*time.Second)
	if cs.Len() != 2 {
		t.Fatalf("len = %d, want 2", cs.Len())
	}
	now = now.Add(30 * time.Second)
	cs.Cleanup()
	if cs.Len() != 1 {
		t.Fatalf("len tras cleanup = %d, want 1 (solo a)", cs.Len())
	}
	if !cs.IsCooling("a") || cs.IsCooling("b") {
		t.Fatal("estados post-cleanup incorrectos")
	}
}

func TestStreamFailoverBeforeFirstByte(t *testing.T) {
	p1 := &streamFake{fakeProvider: newFake("p1", "m")}
	p2 := &streamFake{fakeProvider: newFake("p2", "m").ok("x"), script: []provider.StreamEvent{{Data: `{"choices":[{"delta":{"content":"hola"}}]}`}, {Data: "[DONE]"}}}
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)

	res, err := r.Stream(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if res.Provider.ID() != "p2" {
		t.Fatalf("provider = %s, want p2 (fallback pre-primer-byte)", res.Provider.ID())
	}
	var datas []string
	for ev := range res.Events {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		datas = append(datas, ev.Data)
	}
	if len(datas) != 2 || datas[1] != "[DONE]" {
		t.Fatalf("datas = %v", datas)
	}
}

func TestStreamAllFail(t *testing.T) {
	p1 := &streamFake{fakeProvider: newFake("p1", "m")}
	p2 := &streamFake{fakeProvider: newFake("p2", "m")}
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, nil)

	_, err := r.Stream(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", ce.Status)
	}
}

func TestStreamClampApplied(t *testing.T) {
	p1 := &streamFake{fakeProvider: newFake("p1", "m").withMaxTokens(1024), script: []provider.StreamEvent{{Data: "[DONE]"}}}
	big := int64(65536)
	// body con stream:true
	body, _ := json.Marshal(map[string]any{
		"model": "m", "stream": true, "max_tokens": big,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	r := New([]ProviderSpec{{Provider: p1}}, 2, time.Minute, 0, 30*time.Second, nil)

	if _, err := r.Stream(context.Background(), reqFor("m"), body); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := p1.lastBodyMaxTokens(); got != 1024 {
		t.Fatalf("stream: max_tokens = %d, want 1024 (clampado)", got)
	}
}

func TestNoProviderServesModelStream(t *testing.T) {
	p1 := newFake("p1", "m1")
	r := New([]ProviderSpec{{Provider: p1}}, 2, time.Minute, 0, 30*time.Second, nil)

	_, err := r.Stream(context.Background(), reqFor("nope"), bodyFor("nope", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Type != "model_not_found" {
		t.Fatalf("type = %s, want model_not_found", ce.Type)
	}
	if strings.Contains(ce.Message, "http") {
		t.Fatalf("mensaje filtra URL: %q", ce.Message)
	}
}

// ---- 005-005-verbose: eventos debug (provider_attempt / provider_fallback
// / cooldown_start / decision) + privacidad ----

func parseLogLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("línea de log no parsea como JSON: %q: %v", ln, err)
		}
		lines = append(lines, m)
	}
	return lines
}

func linesWithMsg(lines []map[string]any, msg string) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if l["msg"] == msg {
			out = append(out, l)
		}
	}
	return out
}

func hasProvider(lines []map[string]any, id string) bool {
	for _, l := range lines {
		if l["provider"] == id {
			return true
		}
	}
	return false
}

func TestDebugEventosFallback(t *testing.T) {
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").ok("de p2")

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, logger)

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	lines := parseLogLines(t, buf.String())

	// provider_attempt: al menos un intento por cada provider del chain.
	attempts := linesWithMsg(lines, "provider_attempt")
	if len(attempts) < 2 {
		t.Fatalf("provider_attempt: want ≥2 (p1 y p2), got %d:\n%s", len(attempts), buf.String())
	}
	if !hasProvider(attempts, "p1") || !hasProvider(attempts, "p2") {
		t.Fatalf("provider_attempt sin p1/p2: %v", attempts)
	}

	// provider_fallback: el provider fallido y el destino.
	fallbacks := linesWithMsg(lines, "provider_fallback")
	if len(fallbacks) == 0 {
		t.Fatalf("sin provider_fallback:\n%s", buf.String())
	}
	fb := fallbacks[0]
	if fb["provider_fallido"] != "p1" {
		t.Fatalf("provider_fallback provider_fallido = %v, want p1", fb["provider_fallido"])
	}
	if fb["proximo_provider"] != "p2" {
		t.Fatalf("provider_fallback proximo_provider = %v, want p2", fb["proximo_provider"])
	}

	// decision: por qué se saltó/eligió un provider.
	if len(linesWithMsg(lines, "decision")) == 0 {
		t.Fatalf("sin evento decision:\n%s", buf.String())
	}

	// cooldown_start: p1 falló retryable con cooldown 60s → entra en cooldown.
	if len(linesWithMsg(lines, "cooldown_start")) == 0 {
		t.Fatalf("sin evento cooldown_start (p1 entró en cooldown):\n%s", buf.String())
	}
}

func TestDebugPrivacidadSinPromptEnLogs(t *testing.T) {
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").ok("de p2")

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := New([]ProviderSpec{{Provider: p1}, {Provider: p2}}, 2, time.Minute, 0, 30*time.Second, logger)

	secretBody := []byte(`{"model":"m","messages":[{"role":"user","content":"S3CRETO-PROMPT-123"}]}`)
	if _, err := r.Complete(context.Background(), reqFor("m"), secretBody); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if logs := buf.String(); strings.Contains(logs, "S3CRETO-PROMPT-123") {
		t.Fatalf("PRIVACIDAD: log debug contiene el prompt:\n%s", logs)
	}
}

// ---- 001-003-fallback-v2: max_retries como tope de intentos CIRCULARES ----

func specsOf(ps ...*fakeProvider) []ProviderSpec {
	specs := make([]ProviderSpec, len(ps))
	for i, p := range ps {
		specs[i] = ProviderSpec{Provider: p}
	}
	return specs
}

func totalCalls(ps ...*fakeProvider) int {
	total := 0
	for _, p := range ps {
		total += p.callCount()
	}
	return total
}

func TestMaxRetriesCircularMultiPasada(t *testing.T) {
	// 3 providers, max_retries=8 → maxAttempts=9 → 3 vueltas completas:
	// intentos 1,4,7 / 2,5,8 / 3,6,9 (el cooldown intra-request NO filtra).
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").fail500()
	p3 := newFake("p3", "m").fail500()
	r := New(specsOf(p1, p2, p3), 8, time.Minute, 0, 30*time.Second, nil)

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("Complete: se esperaba error (todos fallan 500)")
	}
	if got := p1.callCount(); got != 3 {
		t.Fatalf("p1 intentos = %d, want 3 (vuelta 1,2,3)", got)
	}
	if got := p2.callCount(); got != 3 {
		t.Fatalf("p2 intentos = %d, want 3 (vuelta 1,2,3)", got)
	}
	if got := p3.callCount(); got != 3 {
		t.Fatalf("p3 intentos = %d, want 3 (vuelta 1,2,3)", got)
	}
	if got := totalCalls(p1, p2, p3); got != 9 {
		t.Fatalf("intentos totales = %d, want 9 (max_retries+1 con recorrido circular)", got)
	}
}

func TestMaxRetriesUnaVueltaCompleta(t *testing.T) {
	// 5 providers, max_retries=4 → maxAttempts=5 → una vuelta completa:
	// cada provider se intenta 1 vez y se llega al último.
	ps := []*fakeProvider{
		newFake("p1", "m").fail500(), newFake("p2", "m").fail500(), newFake("p3", "m").fail500(),
		newFake("p4", "m").fail500(), newFake("p5", "m").fail500(),
	}
	r := New(specsOf(ps...), 4, time.Minute, 0, 30*time.Second, nil)

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("Complete: se esperaba error (todos fallan 500)")
	}
	for i, p := range ps {
		if got := p.callCount(); got != 1 {
			t.Fatalf("p%d intentos = %d, want 1 (una vuelta)", i+1, got)
		}
	}
	if got := totalCalls(ps...); got != 5 {
		t.Fatalf("intentos totales = %d, want 5", got)
	}
}

func TestMaxRetriesCircularLlegaAlUltimo(t *testing.T) {
	// 5 providers (p1-p4 fallan, p5 vivo), max_retries=4 → 5 intentos:
	// el proxy agota la cadena en UN request y llega al provider vivo del final,
	// sin depender del retry del cliente.
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").fail500()
	p3 := newFake("p3", "m").fail500()
	p4 := newFake("p4", "m").fail500()
	p5 := newFake("p5", "m").ok("exito")
	r := New(specsOf(p1, p2, p3, p4, p5), 4, time.Minute, 0, 30*time.Second, nil)

	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Complete: %v (p5 vivo debe responder en el mismo request)", err)
	}
	if got := res.Response.Choices[0].Message.Content; got != "exito" {
		t.Fatalf("content = %q, want exito", got)
	}
	if got := p5.callCount(); got != 1 {
		t.Fatalf("p5 intentos = %d, want 1", got)
	}
	if got := totalCalls(p1, p2, p3, p4, p5); got != 5 {
		t.Fatalf("intentos totales = %d, want 5 (una vuelta completa)", got)
	}
}

func TestMaxRetriesCeroUnIntento(t *testing.T) {
	// max_retries=0 → maxAttempts=1 → solo se intenta el primer provider.
	ps := []*fakeProvider{
		newFake("p1", "m").fail500(), newFake("p2", "m").fail500(), newFake("p3", "m").fail500(),
		newFake("p4", "m").fail500(), newFake("p5", "m").fail500(),
	}
	r := New(specsOf(ps...), 0, time.Minute, 0, 30*time.Second, nil)

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("Complete: se esperaba error (el único intento falla)")
	}
	if got := ps[0].callCount(); got != 1 {
		t.Fatalf("p1 intentos = %d, want 1", got)
	}
	if got := totalCalls(ps...); got != 1 {
		t.Fatalf("intentos totales = %d, want 1 (max_retries=0 → 1 solo intento)", got)
	}
}

func TestCircularNoRetryableCorta(t *testing.T) {
	// 4xx de cliente → responde ya; el loop circular NO reintenta
	// errores no-retryable (p2 no se toca aunque haya presupuesto).
	p1 := newFake("p1", "m").fail400()
	p2 := newFake("p2", "m").ok("de p2")
	r := New(specsOf(p1, p2), 5, time.Minute, 0, 30*time.Second, nil)

	_, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", ce.Status)
	}
	if got := p2.callCount(); got != 0 {
		t.Fatalf("p2 intentos = %d, want 0 (no-retryable no reintenta)", got)
	}
}

// ---- 002-001-retry: reintentos sobre el MISMO provider con backoff ----

// healthFake es un checker programable para el health.Store (002-002).
type healthFake struct {
	id  string
	mu  sync.Mutex
	err error
}

func (h *healthFake) ID() string { return h.id }
func (h *healthFake) Health(ctx context.Context) (time.Duration, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return 0, h.err
	}
	return time.Millisecond, nil
}
func (h *healthFake) setErr(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.err = err
}

func newHealthStore(t *testing.T, states map[string]error) *health.Store {
	t.Helper()
	st := health.New(time.Second, time.Second, nil)
	for id, err := range states {
		hf := &healthFake{id: id, err: err}
		st.Register(hf)
		st.CheckNow()
	}
	return st
}

func TestRetrySameProviderAbsorbsTransient(t *testing.T) {
	// 002-001: un 500 transitorio se absorbe con reintentos sobre el
	// MISMO provider — p2 nunca se toca.
	p1 := newFake("p1", "m").failThenOK(1, &provider.ErrUpstream{StatusCode: 500, Type: "upstream_error", Message: "transient"})
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Retry: RetryConfig{MaxAttempts: 2, BackoffBase: time.Millisecond, BackoffMax: 10 * time.Millisecond},
	})

	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Complete: %v (el reintento debió absorber el 500 transitorio)", err)
	}
	if res.ProviderID != "p1" {
		t.Fatalf("provider = %s, want p1 (sin fallback a p2)", res.ProviderID)
	}
	if got := p1.callCount(); got != 2 {
		t.Fatalf("p1 intentos = %d, want 2 (1 fallo + 1 reintento)", got)
	}
	if got := p2.callCount(); got != 0 {
		t.Fatalf("p2 intentos = %d, want 0 (el fallo transitorio no debe llegar a p2)", got)
	}
}

func TestRetryExhaustsThenFallback(t *testing.T) {
	// 002-001: un fallo PERSISTENTE agota MaxAttempts y recién ahí cede
	// a p2 (comportamiento 001-003 intacto).
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Retry: RetryConfig{MaxAttempts: 3, BackoffBase: time.Millisecond, BackoffMax: 10 * time.Millisecond},
	})

	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2", res.ProviderID)
	}
	if got := p1.callCount(); got != 3 {
		t.Fatalf("p1 intentos = %d, want 3 (MaxAttempts agotados)", got)
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	// 002-001 verification: 100 muestras por n → backoff siempre dentro
	// de [base*2^n*0.8, base*2^n*1.2].
	r := NewWithOptions(nil, Options{})
	r.randSource = rand.New(rand.NewSource(42))
	base := 100 * time.Millisecond
	cfg := RetryConfig{BackoffBase: base, BackoffMax: time.Minute}

	for n := 0; n < 4; n++ {
		lo := base * time.Duration(1<<n) * 8 / 10
		hi := base * time.Duration(1<<n) * 12 / 10
		for i := 0; i < 100; i++ {
			d := r.backoffFor(cfg, n, nil)
			if d < lo || d > hi {
				t.Fatalf("backoff n=%d = %v, fuera de [%v, %v]", n, d, lo, hi)
			}
		}
	}
}

func TestBackoffRespectsRetryAfter(t *testing.T) {
	// 002-001: un 429 con Retry-After usa ese valor (tope backoff_max).
	r := NewWithOptions(nil, Options{})
	cfg := RetryConfig{BackoffBase: time.Millisecond, BackoffMax: 5 * time.Second}
	ue := &provider.ErrUpstream{StatusCode: 429, Type: "upstream_error", Message: "slow down", RetryAfter: 5 * time.Second}
	d := r.backoffFor(cfg, 3, ue)
	if d != 5*time.Second {
		t.Fatalf("backoff = %v, want 5s (Retry-After)", d)
	}
}

func TestBackoffCapsAtMax(t *testing.T) {
	r := NewWithOptions(nil, Options{})
	cfg := RetryConfig{BackoffBase: time.Second, BackoffMax: 5 * time.Second}
	// n=10 → base*2^10 = ~17min → tope 5s
	if d := r.backoffFor(cfg, 10, nil); d != 5*time.Second {
		t.Fatalf("backoff = %v, want 5s (tope backoff_max)", d)
	}
}

func TestBackoffZeroWhenNoBase(t *testing.T) {
	r := NewWithOptions(nil, Options{})
	if d := r.backoffFor(RetryConfig{}, 3, nil); d != 0 {
		t.Fatalf("backoff = %v, want 0 (sin base)", d)
	}
}

func TestRetry4xxNeverRetried(t *testing.T) {
	// 002-001: 4xx de cliente NO se reintenta jamás — 1 solo intento.
	p1 := newFake("p1", "m").fail400()
	r := NewWithOptions(specsOf(p1), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Retry: RetryConfig{MaxAttempts: 5, BackoffBase: time.Millisecond, BackoffMax: 10 * time.Millisecond},
	})

	_, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) || ce.Status != http.StatusBadRequest {
		t.Fatalf("err = %v, want ChainError 400", err)
	}
	if got := p1.callCount(); got != 1 {
		t.Fatalf("p1 intentos = %d, want 1 (4xx nunca se reintenta)", got)
	}
}

// ---- 002-002-health: saltear providers unhealthy (soft) ----

func TestHealthSkipsUnhealthyProvider(t *testing.T) {
	// p1 unhealthy (health check) + p2 healthy → el request va a p2 sin
	// intentar p1.
	hs := newHealthStore(t, map[string]error{"p1": errors.New("down"), "p2": nil})
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Health: hs,
	})

	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (p1 unhealthy se saltea)", res.ProviderID)
	}
	if got := p1.callCount(); got != 0 {
		t.Fatalf("p1 intentos = %d, want 0 (salteado por health)", got)
	}
}

func TestHealthSoftWhenAllUnhealthy(t *testing.T) {
	// Todos unhealthy → igual se intentan en orden (marca SOFT): p1
	// responde y el request tiene éxito.
	hs := newHealthStore(t, map[string]error{"p1": errors.New("down"), "p2": errors.New("down")})
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Health: hs,
	})

	res, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if err != nil {
		t.Fatalf("Complete: %v (soft: igual intenta unhealthy)", err)
	}
	if res.ProviderID != "p1" {
		t.Fatalf("provider = %s, want p1 (orden de config)", res.ProviderID)
	}
	if got := p1.callCount(); got != 1 {
		t.Fatalf("p1 intentos = %d, want 1", got)
	}
}

// ---- 002-004-degradacion: fast-fail cuando no hay providers ----

func TestDegradationAllProvidersDown(t *testing.T) {
	// Todos en cooldown (fallaron en el request 1) → request 2 hace
	// fast-fail 502 all_providers_down sin recorrer la cadena.
	p1 := newFake("p1", "m").fail500()
	p2 := newFake("p2", "m").fail500()
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Degradation: DegradationConfig{MaxRequests: 0, CooldownGrace: 0},
	})

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("request 1: se esperaba error (todos fallan)")
	}
	// Request 2: ambos en cooldown → fast-fail directo, 0 intentos nuevos.
	_, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusBadGateway || ce.Code != "all_providers_down" {
		t.Fatalf("status/code = %d/%s, want 502/all_providers_down", ce.Status, ce.Code)
	}
	if got := p1.callCount(); got != 1 {
		t.Fatalf("p1 intentos totales = %d, want 1 (fast-fail no reintenta)", got)
	}
}

func TestDegradationLimit503(t *testing.T) {
	// degraded_max_requests=1 → el 1er fast-fail es 502; el 2do ya es
	// 503 degraded_limit.
	p1 := newFake("p1", "m").fail500()
	r := NewWithOptions(specsOf(p1), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Degradation: DegradationConfig{MaxRequests: 1, CooldownGrace: 0},
	})

	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("request 1: se esperaba error (intento real)")
	}
	// Fast-fail 1: cnt=1, 1 > 1 es falso → 502 all_providers_down.
	_, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("request 2: err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusBadGateway || ce.Code != "all_providers_down" {
		t.Fatalf("request 2: status/code = %d/%s, want 502/all_providers_down", ce.Status, ce.Code)
	}
	// Fast-fail 2: cnt=2 > 1 → 503 degraded_limit.
	_, err = r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	if !errors.As(err, &ce) {
		t.Fatalf("request 3: err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusServiceUnavailable || ce.Code != "degraded_limit" {
		t.Fatalf("request 3: status/code = %d/%s, want 503/degraded_limit", ce.Status, ce.Code)
	}
}

func TestDegradationRecovers(t *testing.T) {
	// Tras un fast-fail, un request exitoso resetea el contador: el
	// próximo fast-fail vuelve a ser 502 (no 503).
	p1 := newFake("p1", "m").fail500()
	r := NewWithOptions(specsOf(p1), Options{
		MaxRetries: 1, Cooldown: 200 * time.Millisecond, GlobalTimeout: 30 * time.Second,
		Degradation: DegradationConfig{MaxRequests: 1, CooldownGrace: 0},
	})
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("request 1: se esperaba error")
	}
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("request 2 (fast-fail): se esperaba error")
	}
	// Cooldown expira → p1 responde OK → resetDegraded.
	time.Sleep(250 * time.Millisecond)
	p1.ok("recuperado")
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err != nil {
		t.Fatalf("request 3: %v (cooldown expirado, p1 sano)", err)
	}
	// p1 falla de nuevo en un intento real → cooldown; luego fast-fail
	// → 502 (contador reseteado por el éxito, NO 503).
	p1.fail500()
	if _, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil)); err == nil {
		t.Fatal("request 4: se esperaba error (intento real)")
	}
	_, err := r.Complete(context.Background(), reqFor("m"), bodyFor("m", nil))
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("request 5: err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusBadGateway || ce.Code != "all_providers_down" {
		t.Fatalf("request 5: status/code = %d/%s, want 502/all_providers_down (contador reseteado)", ce.Status, ce.Code)
	}
}
