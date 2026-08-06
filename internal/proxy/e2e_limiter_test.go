package proxy_test

// Tests EPIC-004 (004-001-concurrencia + 004-002-aislamiento):
// límites de concurrencia globales (backpressure con timeout → 429) y
// aislamiento por cliente/agente (fast-fail 429 sin afectar a otros).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/limiter"
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// slowUpstream responde 200 tras un delay configurable (para mantener
// slots ocupados). Captura headers para verificar que X-Agent-Id no
// viaja upstream.
type slowUpstream struct {
	delay      time.Duration
	stream     bool // responder como SSE
	gotHeaders http.Header
	mu         sync.Mutex
}

func (u *slowUpstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.gotHeaders = r.Header.Clone()
		u.mu.Unlock()
		if u.delay > 0 {
			time.Sleep(u.delay)
		}
		stream := u.stream
		if stream {
			// Respeta el flag del request: un cliente no-stream que recibe
			// SSE es un error de upstream real, no un caso de test.
			raw, _ := io.ReadAll(r.Body)
			var req struct {
				Stream bool `json:"stream"`
			}
			_ = json.Unmarshal(raw, &req)
			stream = req.Stream
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"id\":\"chatcmpl-s1\",\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"ho\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"chatcmpl-slow","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":3}}`)
	}
}

func (u *slowUpstream) headers() http.Header {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.gotHeaders
}

// buildLimited construye el proxy con límites de concurrencia custom.
// clients: map clientID -> API key. ups: upstreams fake.
func buildLimited(t *testing.T, clients map[string]string, globalMax int, backpressure time.Duration, budgets map[string]limiter.ClientBudget, ups ...*slowUpstream) (*httptest.Server, *metrics.Metrics) {
	t.Helper()
	specs := make([]router.ProviderSpec, len(ups))
	providers := make([]provider.Provider, len(ups))
	for i, u := range ups {
		srv := httptest.NewServer(u.handler())
		t.Cleanup(srv.Close)
		cl := provider.NewClient("up"+string(rune('1'+i)), srv.URL, "sk-upstream", []string{"m"}, 4096, nil)
		providers[i] = cl
		specs[i] = router.ProviderSpec{Provider: cl}
	}
	r := router.New(specs, 2, 0, 0, 30*time.Second, nil)

	authClients := make([]auth.Client, 0, len(clients))
	for id, key := range clients {
		authClients = append(authClients, auth.Client{ID: id, KeySHA256: auth.HashKey(key)})
	}
	authz := auth.New(authClients)
	m := metrics.New()

	var globalLimiter *limiter.Limiter
	if globalMax > 0 {
		globalLimiter = limiter.New(globalMax)
	}
	var keyedLimiter *limiter.Keyed
	if len(budgets) > 0 {
		keyedLimiter = limiter.NewKeyed(budgets)
	}

	s := proxy.New(r, providers, authz, m, nil, 10<<20, globalLimiter, keyedLimiter, backpressure)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, m
}

// chatWith envía un chat request con key y header opcional X-Agent-Id.
func chatWith(t *testing.T, srv *httptest.Server, key, agentID string) (*http.Response, []byte) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	if agentID != "" {
		req.Header.Set("X-Agent-Id", agentID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// holdSlot lanza un request que ocupa un slot y devuelve el canal con
// su status code.
func holdSlot(t *testing.T, srv *httptest.Server, key, agentID string) chan int {
	t.Helper()
	done := make(chan int, 1)
	go func() {
		resp, _ := chatWith(t, srv, key, agentID)
		done <- resp.StatusCode
	}()
	return done
}

func TestE2EGlobalLimiterWaitsAndSucceeds(t *testing.T) {
	up := &slowUpstream{delay: 300 * time.Millisecond}
	srv, _ := buildLimited(t, map[string]string{"c1": "sk-1"}, 1, 5*time.Second, nil, up)

	// Request 1 ocupa el slot 300ms; request 2 lanzado a los 50ms debe
	// esperar y completar OK (backpressure, no 429).
	var wg sync.WaitGroup
	results := make([]int, 2)
	start := time.Now()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, _ := chatWith(t, srv, "sk-1", "")
			results[i] = resp.StatusCode
		}(i)
		time.Sleep(50 * time.Millisecond)
	}
	wg.Wait()
	elapsed := time.Since(start)
	for i, code := range results {
		if code != 200 {
			t.Fatalf("request %d: status = %d, want 200 (espera por slot)", i, code)
		}
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("requests serializados por el slot: elapsed = %v, want >= 300ms", elapsed)
	}
}

func TestE2EGlobalLimiterTimeout429(t *testing.T) {
	up := &slowUpstream{delay: 5 * time.Second} // ocupa el slot mucho más que el backpressure
	srv, m := buildLimited(t, map[string]string{"c1": "sk-1"}, 1, 200*time.Millisecond, nil, up)

	// Request 1 ocupa el slot en background (delay 5s lo mantiene ocupado).
	done1 := holdSlot(t, srv, "sk-1", "")
	time.Sleep(100 * time.Millisecond) // el slot ya está ocupado

	// Request 2 espera 200ms (backpressure_timeout) y recibe 429.
	start := time.Now()
	resp2, body2 := chatWith(t, srv, "sk-1", "")
	elapsed := time.Since(start)
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request 2: status = %d, want 429 rate_limit_exceeded; body=%s", resp2.StatusCode, body2)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("el 429 llegó antes del backpressure_timeout: elapsed=%v", elapsed)
	}
	if !strings.Contains(string(body2), "rate_limit_exceeded") {
		t.Fatalf("body sin rate_limit_exceeded: %s", body2)
	}
	// Métrica registrada con reason global_timeout.
	if !strings.Contains(m.Render(), `mofgw_rejected_requests_total{client="c1",reason="global_timeout"} 1`) {
		t.Fatalf("métrica global_timeout ausente:\n%s", m.Render())
	}
	// Liberar request 1 para que el test no cuelgue.
	select {
	case code := <-done1:
		if code != 200 {
			t.Fatalf("request 1: status = %d, want 200", code)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("request 1 no terminó")
	}
}

func TestE2EClientLimitIsolation(t *testing.T) {
	up := &slowUpstream{delay: 300 * time.Millisecond}
	budgets := map[string]limiter.ClientBudget{
		"c1": {MaxConcurrent: 1, MaxPerAgent: 0},
		"c2": {MaxConcurrent: 1, MaxPerAgent: 0},
	}
	srv, _ := buildLimited(t, map[string]string{"c1": "sk-1", "c2": "sk-2"}, 0, 0, budgets, up)

	// c1 ocupa su slot en background (delay 300ms).
	done1 := holdSlot(t, srv, "sk-1", "")
	time.Sleep(100 * time.Millisecond)

	// Segundo request de c1 → 429 inmediato (fast-fail).
	start := time.Now()
	resp2, body2 := chatWith(t, srv, "sk-1", "")
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("c1 r2 debía ser fast-fail, tardó %v", elapsed)
	}
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("c1 r2: status = %d, want 429; body=%s", resp2.StatusCode, body2)
	}
	// c2 NO se ve afectado por la saturación de c1 (aislamiento).
	resp3, _ := chatWith(t, srv, "sk-2", "")
	if resp3.StatusCode != 200 {
		t.Fatalf("c2 r1: status = %d, want 200 (aislamiento entre clientes)", resp3.StatusCode)
	}
	select {
	case code := <-done1:
		if code != 200 {
			t.Fatalf("c1 r1: status = %d, want 200", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("c1 r1 no terminó")
	}
}

func TestE2EAgentLimit(t *testing.T) {
	up := &slowUpstream{delay: 300 * time.Millisecond}
	budgets := map[string]limiter.ClientBudget{
		"c1": {MaxConcurrent: 0, MaxPerAgent: 1},
	}
	srv, _ := buildLimited(t, map[string]string{"c1": "sk-1"}, 0, 0, budgets, up)

	// (c1, agent-a) ocupa su slot en background.
	done1 := holdSlot(t, srv, "sk-1", "agent-a")
	time.Sleep(100 * time.Millisecond)

	// Mismo agente → 429 fast-fail.
	resp2, _ := chatWith(t, srv, "sk-1", "agent-a")
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("(c1,agent-a) r2: status = %d, want 429", resp2.StatusCode)
	}
	// Otro agente del mismo cliente → OK.
	resp3, _ := chatWith(t, srv, "sk-1", "agent-b")
	if resp3.StatusCode != 200 {
		t.Fatalf("(c1,agent-b): status = %d, want 200", resp3.StatusCode)
	}
	select {
	case code := <-done1:
		if code != 200 {
			t.Fatalf("(c1,agent-a) r1: status = %d, want 200", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("(c1,agent-a) r1 no terminó")
	}
}

func TestE2ENoLimitsDefault(t *testing.T) {
	// Sin límites configurados → N requests simultáneos pasan (regresión).
	up := &slowUpstream{delay: 50 * time.Millisecond}
	srv, _ := buildLimited(t, map[string]string{"c1": "sk-1"}, 0, 0, nil, up)
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, _ := chatWith(t, srv, "sk-1", "")
			codes[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		if code != 200 {
			t.Fatalf("request %d sin límites: status = %d, want 200", i, code)
		}
	}
}

func TestE2EXAgentIDNotForwarded(t *testing.T) {
	up := &slowUpstream{}
	srv, _ := buildLimited(t, map[string]string{"c1": "sk-1"}, 0, 0, nil, up)
	resp, _ := chatWith(t, srv, "sk-1", "secreto-interno")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := up.headers().Get("X-Agent-Id"); got != "" {
		t.Fatalf("X-Agent-Id %q llegó upstream — el header es de SOLO lectura (004-002)", got)
	}
	if got := up.headers().Get("Authorization"); got == "" {
		t.Fatal("Authorization de provider debería viajar upstream")
	}
}

func TestE2EMetricsAgentLimitLabel(t *testing.T) {
	up := &slowUpstream{delay: 300 * time.Millisecond}
	budgets := map[string]limiter.ClientBudget{
		"c1": {MaxConcurrent: 0, MaxPerAgent: 1},
	}
	srv, m := buildLimited(t, map[string]string{"c1": "sk-1"}, 0, 0, budgets, up)
	done1 := holdSlot(t, srv, "sk-1", "agent-a") // ocupa
	time.Sleep(100 * time.Millisecond)
	_, _ = chatWith(t, srv, "sk-1", "agent-a") // 429
	select {
	case <-done1:
	case <-time.After(3 * time.Second):
		t.Fatal("r1 no terminó")
	}
	rendered := m.Render()
	if !strings.Contains(rendered, `mofgw_rejected_requests_total{client="c1/agent-a",reason="agent_limit"} 1`) {
		t.Fatalf("métrica agent_limit ausente:\n%s", rendered)
	}
}

// TestE2EStreamingHoldsSlot asegura que un stream ocupa el slot hasta
// terminar (un stream en curso bloquea el siguiente si el límite es 1).
func TestE2EStreamingHoldsSlot(t *testing.T) {
	// upstream SSE con delay: el primer request (stream) mantiene el slot.
	up := &slowUpstream{delay: 300 * time.Millisecond, stream: true}
	srv, _ := buildLimited(t, map[string]string{"c1": "sk-1"}, 1, 5*time.Second, nil, up)

	raw, _ := json.Marshal(map[string]any{"model": "m", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer sk-1")
	done := make(chan int, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("stream request: %v", err)
			done <- 0
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		done <- resp.StatusCode
	}()
	time.Sleep(50 * time.Millisecond) // stream ya arrancó y ocupa el slot

	// Segundo request espera hasta backpressure_timeout (5s) — el stream
	// termina a los ~300ms, así que debe pasar con 200.
	start := time.Now()
	resp2, _ := chatWith(t, srv, "sk-1", "")
	if resp2.StatusCode != 200 {
		t.Fatalf("request 2: status = %d, want 200 (esperó a que el stream libere el slot)", resp2.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("request 2 esperó demasiado: %v", elapsed)
	}
	select {
	case code := <-done:
		if code != 200 {
			t.Fatalf("stream status = %d, want 200", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream no terminó")
	}
}
