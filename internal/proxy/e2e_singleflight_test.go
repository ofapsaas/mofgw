// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED del coalescing single-flight (010-001 P0) aplicado a los
// FOLLOWERS: un request idéntico concurrente que se une a un vuelo ya en
// curso (en vez de hacer su propia llamada upstream) debe, igual que el
// líder:
//
//	(a) facturar el consumo a SU clientID (006-001 P2 — el dedupe no
//	    exime al cliente del costo del resultado que recibe), y
//	(b) exponer X-Usage-* en SU response (007-003 P1).
//
// Contrato observable: headers X-Usage-Total-Tokens del response y label
// client="client-<key>" en el desagregado de /metrics. Cero red externa:
// providers fake en httptest con un upstream que bloquea al líder para
// garantizar la superposición líder/follower.

package proxy_test

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
	"github.com/ofapsaas/mofgw/internal/metrics"
	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// sfGate coordina la superposición líder/follower de un vuelo
// single-flight: el upstream del líder se bloquea hasta que el test
// cierra release, garantizando que el follower llegue y se una al vuelo
// mientras el líder aún está en curso (condición necesaria para que el
// follower NO haga su propia llamada upstream y comparta el blob).
type sfGate struct {
	arrived chan struct{} // líder ya entró al upstream (vuelo en curso)
	release chan struct{} // test suelta el upstream del líder
	once    sync.Once
}

func (g *sfGate) waitLeaderArrived(t *testing.T) {
	t.Helper()
	select {
	case <-g.arrived:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout esperando que el líder llegue al upstream (vuelo no arrancó)")
	}
}

// buildSF construye el proxy con single-flight ON (SetSingleFlight(true))
// y un upstream que bloquea el primer /chat/completions hasta release.
// Único upstream: el líder se queda en vuelo; el follower, al ser
// determinístico (temperature=0), se une al vuelo sin llamar upstream.
func buildSF(t *testing.T, gate *sfGate, clientKeys ...string) *harness {
	t.Helper()
	h := &harness{key: clientKeys[0]}
	ups := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"m"}]}`)
			return
		}
		// primer chat: señal + bloqueo (el líder se queda en vuelo)
		gate.once.Do(func() { close(gate.arrived) })
		<-gate.release
		w.Header().Set("Content-Type", "application/json")
		// usage prompt 1200, completion 80, total 1280, cached 1000
		io.WriteString(w, upstreamUsageOK("m").body)
	}))
	t.Cleanup(ups.Close)
	cl := provider.NewClient("up1", ups.URL, "sk-upstream", []string{"m"}, 4096, nil)
	r := router.New([]router.ProviderSpec{{Provider: cl}}, 2, 0, 0, 30*1000*1000*1000, nil)
	clients := make([]auth.Client, 0, len(clientKeys))
	for _, k := range clientKeys {
		clients = append(clients, auth.Client{ID: "client-" + k, KeySHA256: auth.HashKey(k)})
	}
	authz := auth.New(clients)
	m := metrics.New()
	s := proxy.New(r, []provider.Provider{cl}, authz, m, nil, nil, 10<<20, nil, nil, 0)
	s.SetSingleFlight(true, 512)
	h.proxySrv = s
	h.srv = httptest.NewServer(s.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

// chatRawNoT envía un chat autenticado como key sin tocar t (seguro para
// correr en goroutine — no llama t.Fatalf). Devuelve (resp, err).
func chatRawNoT(srvURL, key string, body map[string]any) (*http.Response, error) {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	return http.DefaultClient.Do(req)
}

// TestSF_FollowerFacturaYExponeUsage: un follower de un vuelo
// single-flight (request idéntico concurrente de OTRO cliente) debe
// facturar el consumo a SU clientID y exponer X-Usage-* en SU response.
// RED para el bug: el follower compartía el blob sin llamar a
// recordCacheTokens ni setUsageHeaders (solo corrían en el líder), así
// que su clientID no se facturaba y sus headers quedaban vacíos.
//
// Condición de validez: la aserción de dedupe
// (mofgw_single_flight_deduped_total) prueba que el follower se unió al
// vuelo del líder (no hizo su propia llamada upstream). Sin esa garantía
// el test pasaría por el camino equivocado.
func TestSF_FollowerFacturaYExponeUsage(t *testing.T) {
	gate := &sfGate{arrived: make(chan struct{}), release: make(chan struct{})}
	h := buildSF(t, gate, "k1", "k2")
	body := map[string]any{
		"model":       "m",
		"temperature": 0, // determinístico → camino single-flight (010-001 P0)
		"messages":    []map[string]string{{"role": "user", "content": "hi"}},
	}

	type result struct {
		resp *http.Response
		err  error
	}
	collect := func(key string, out chan<- result) {
		r, err := chatRawNoT(h.srv.URL, key, body)
		out <- result{r, err}
	}

	// líder (k1): se bloquea en el upstream, manteniendo el vuelo
	leaderCh := make(chan result, 1)
	followerCh := make(chan result, 1)
	go collect("k1", leaderCh)
	gate.waitLeaderArrived(t) // vuelo en curso (líder en upstream)
	// follower (k2): request idéntico concurrente → se une al vuelo
	go collect("k2", followerCh)

	time.Sleep(200 * time.Millisecond) // deja que el follower se una al vuelo
	close(gate.release)                // ambos completan con el blob compartido

	// líder
	var lr *http.Response
	select {
	case res := <-leaderCh:
		if res.err != nil {
			t.Fatalf("leader request: %v", res.err)
		}
		lr = res.resp
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout esperando al líder")
	}
	defer lr.Body.Close()
	if lr.StatusCode != 200 {
		t.Fatalf("leader status = %d, want 200", lr.StatusCode)
	}
	if got := lr.Header.Get("X-Usage-Total-Tokens"); got != "1280" {
		t.Fatalf("leader X-Usage-Total-Tokens = %q, want 1280", got)
	}

	// follower
	var fr *http.Response
	select {
	case res := <-followerCh:
		if res.err != nil {
			t.Fatalf("follower request: %v", res.err)
		}
		fr = res.resp
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout esperando al follower")
	}
	defer fr.Body.Close()
	if fr.StatusCode != 200 {
		t.Fatalf("follower status = %d, want 200", fr.StatusCode)
	}
	// (b) follower: X-Usage-* en SU response (bug: quedan vacíos)
	if got := fr.Header.Get("X-Usage-Total-Tokens"); got != "1280" {
		t.Fatalf("follower X-Usage-Total-Tokens = %q, want 1280 (bug: follower sin setUsageHeaders)", got)
	}

	s := h.metricsBody(t)
	// el follower fue coalescido: condición de validez de la prueba
	if !strings.Contains(s, `mofgw_single_flight_deduped_total{client="client-k2"} 1`) {
		t.Fatalf("follower no se deduplicó (test inválido):\n%s", s)
	}
	// (a) ambos clientes facturados (dedupe no exime del costo)
	if !strings.Contains(s, `client="client-k1"`) || !strings.Contains(s, `client="client-k2"`) {
		t.Fatalf("facturación de follower o líder ausente:\n%s", s)
	}
}
