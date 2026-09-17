// Package proxy_test — RED 020-001-registry-cost-model (etapa 3.1).
//
// Tests B1-B9 del test-audit aprobado: enriquecimiento de TerminalEvent con
// `model` + `cost_usd_src` + `cost_usd_up`, captura de `usage.cost` upstream
// y precedencia upstream > tabla > none.
//
// Harness 014-001 reutilizado SIN modificar: buildRegistry + SetRegistry +
// waitRegistryLines + h.chat + tipo `upstream` (fakeUpstream).
package proxy_test

import (
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/proxy"
	"github.com/ofapsaas/mofgw/internal/router"
)

// ---- fixtures (audit §6) ----

// usage canónico B5: prompt 1200, completion 80, cached 1000 → miss 200.
// Con pricing {1.0,1.0,1.0}: 200/1e6 + 80/1e6 + 1000/1e6 = 0.00128.
const costUsageCanon = `"prompt_tokens":1200,"completion_tokens":80,"total_tokens":1280,"prompt_tokens_details":{"cached_tokens":1000}`

// upstreamUsageCost responde 200 con usage canónico + `"cost":<cost>` cuando
// withCost es true (OpenRouter-shaped). Reutiliza el tipo `upstream`
// existente (implementa fakeUpstream vía handler()).
func upstreamUsageCost(model string, cost float64, withCost bool) *upstream {
	costFrag := ""
	if withCost {
		costFrag = `,"cost":` + ftoa(cost)
	}
	return &upstream{status: 200, model: model, body: `{"id":"chatcmpl-cost","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{` + costUsageCanon + costFrag + `}}`}
}

// upstreamUsageCostStream: variante SSE con el costo en el chunk final de usage.
func upstreamUsageCostStream(model string, cost float64, withCost bool) *upstream {
	costFrag := ""
	if withCost {
		costFrag = `,"cost":` + ftoa(cost)
	}
	return &upstream{status: 200, stream: true, model: model, body: "data: {\"id\":\"chatcmpl-s1\",\"model\":\"" + model + "\",\"choices\":[{\"delta\":{\"content\":\"ho\"}}]}\n\n" +
		"data: {\"choices\":[]}\n\n" +
		"data: {\"choices\":[],\"usage\":{" + costUsageCanon + costFrag + "}}\n\n" +
		"data: [DONE]\n\n"}
}

func ftoa(f float64) string {
	return strings.TrimRight(strings.TrimRight(sprintfFloat(f), "0"), ".")
}

func sprintfFloat(f float64) string {
	// Representación exacta corta sin notación científica para el fixture.
	s := ""
	neg := f < 0
	if neg {
		f = -f
		s = "-"
	}
	// 6 decimales alcanzan para los costos del fixture (0.0042, 0.0).
	const prec = 1000000.0
	v := int64(f*prec + 0.5)
	return s + itoa(int(v)/1000000) + "." + pad6(int(v)%1000000)
}

func pad6(n int) string {
	s := itoa(n)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}

func costRegistryOptions() router.Options {
	return router.Options{MaxRetries: 2, Cooldown: 0, GlobalTimeout: 30 * time.Second}
}

func chatHello(t *testing.T, h *harness, model string) *http.Response {
	t.Helper()
	return h.chat(t, map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
}

// terminalSuccessOf espera 1 attempt + 1 terminal y devuelve el terminal.
func terminalSuccessOf(t *testing.T, regPath string) jsonLogLine {
	t.Helper()
	lines := waitRegistryLines(t, regPath, 2)
	if len(lines) != 2 {
		t.Fatalf("líneas = %d, want 2 (1 attempt + 1 terminal):\n%s", len(lines), readRegistry(t, regPath))
	}
	te := terminalByType(lines)
	if te == nil {
		t.Fatalf("sin terminal en:\n%s", readRegistry(t, regPath))
	}
	return te
}

// terminalByType devuelve la primera línea de tipo "terminal".
func terminalByType(lines []jsonLogLine) jsonLogLine {
	for _, l := range lines {
		if l["type"] == "terminal" {
			return l
		}
	}
	return nil
}

func closeBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return raw
}

// TestPostcondition1_TerminalIncluyeModel verifica P1: el TerminalEvent
// incluye `model` con el modelo solicitado por el cliente.
func TestPostcondition1_TerminalIncluyeModel(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageOK("m")}, "sk-test-1", regPath, costRegistryOptions())

	resp := chatHello(t, h, "m")
	closeBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	te := terminalSuccessOf(t, regPath)
	if te["model"] != "m" {
		t.Fatalf("terminal model = %v, want %q (P1)", te["model"], "m")
	}
}

// TestPostcondition2_4_UpstreamSrcYCostoExacto verifica P2 + P4 (C2): con
// `usage.cost` upstream, `cost_usd` es el valor exacto (la tabla NO
// participa aunque haya pricing cargado) y `cost_usd_src="upstream"`.
// Incluye B4: el pricing cargado difiere a propósito del costo upstream.
func TestPostcondition2_4_UpstreamSrcYCostoExacto(t *testing.T) {
	cases := []struct {
		name string
		up   *upstream
	}{
		{"nostream", upstreamUsageCost("m", 0.0042, true)},
		{"stream", upstreamUsageCostStream("m", 0.0042, true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			regPath := filepath.Join(t.TempDir(), "registry.jsonl")
			h, _, _ := buildRegistry(t, []fakeUpstream{tc.up}, "sk-test-1", regPath, costRegistryOptions())
			// Trampa anti-tabla: pricing cargado con valor deliberadamente
			// distinto del costo upstream (tabla daría 0.00128).
			h.proxySrv.SetPricing(map[string]proxy.ModelPricing{"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0}})

			body := map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}}
			if tc.up.stream {
				body["stream"] = true
			}
			resp := h.chat(t, body)
			closeBody(t, resp)
			if resp.StatusCode != 200 {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			te := terminalSuccessOf(t, regPath)
			if te["cost_usd_src"] != "upstream" {
				t.Fatalf("cost_usd_src = %v, want %q (P2)", te["cost_usd_src"], "upstream")
			}
			got, ok := te["cost_usd"].(float64)
			if !ok || math.Abs(got-0.0042) > 1e-12 {
				t.Fatalf("cost_usd = %v, want exactamente 0.0042 (P4, sin re-cálculo)", te["cost_usd"])
			}
			up, ok := te["cost_usd_up"].(float64)
			if !ok || math.Abs(up-0.0042) > 1e-12 {
				t.Fatalf("cost_usd_up = %v, want 0.0042 (P2/P3)", te["cost_usd_up"])
			}
		})
	}
}

// TestPostcondition3_CostoCeroUpstreamNoEsNull verifica P3: un `cost:0.0`
// legítimo (modelo free) se preserva como puntero no-nil y NO se conflaciona
// con "sin dato" (null). Discriminante de B6.
func TestPostcondition3_CostoCeroUpstreamNoEsNull(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageCost("m", 0.0, true)}, "sk-test-1", regPath, costRegistryOptions())

	resp := chatHello(t, h, "m")
	closeBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	te := terminalSuccessOf(t, regPath)
	raw, present := te["cost_usd_up"]
	if !present || raw == nil {
		t.Fatalf("cost_usd_up ausente o null con cost upstream 0.0 — 0.0 legítimo conflacionado con sin-dato (P3)")
	}
	up, ok := raw.(float64)
	if !ok || up != 0.0 {
		t.Fatalf("cost_usd_up = %v (%T), want 0.0 no-nil (P3)", raw, raw)
	}
	if te["cost_usd_src"] != "upstream" {
		t.Fatalf("cost_usd_src = %v, want %q (P2)", te["cost_usd_src"], "upstream")
	}
}

// TestPostcondition5_TablaFallback verifica P5 (rama table): sin costo
// upstream y modelo CON precio, `cost_usd` = estimateCost con la tabla
// (0.00128 canónico), `src="table"`, `up=null`.
func TestPostcondition5_TablaFallback(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageCost("m", 0, false)}, "sk-test-1", regPath, costRegistryOptions())
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0}})

	resp := chatHello(t, h, "m")
	closeBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	te := terminalSuccessOf(t, regPath)
	if te["cost_usd_src"] != "table" {
		t.Fatalf("cost_usd_src = %v, want %q (P5 rama table)", te["cost_usd_src"], "table")
	}
	got, ok := te["cost_usd"].(float64)
	if !ok || math.Abs(got-0.00128) > 1e-9 {
		t.Fatalf("cost_usd = %v, want 0.00128 = estimateCost(tabla) (P5)", te["cost_usd"])
	}
	if raw, present := te["cost_usd_up"]; present && raw != nil {
		t.Fatalf("cost_usd_up = %v, want null sin costo upstream (P3)", raw)
	}
}

// TestPostcondition5_SinPrecioEsNone verifica P5 (rama none): sin costo
// upstream y modelo SIN precio, `cost_usd=0` (dato faltante, no costo cero)
// y `src="none"`. Incluye subcaso error: terminal ERROR también lleva
// `model` (decisión HITL (a): emitTerminalError extendido) con src none.
func TestPostcondition5_SinPrecioEsNone(t *testing.T) {
	t.Run("success_sin_precio", func(t *testing.T) {
		regPath := filepath.Join(t.TempDir(), "registry.jsonl")
		// Sin SetPricing: "m" no está en la tabla.
		h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageCost("m", 0, false)}, "sk-test-1", regPath, costRegistryOptions())

		resp := chatHello(t, h, "m")
		closeBody(t, resp)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}

		te := terminalSuccessOf(t, regPath)
		if te["cost_usd_src"] != "none" {
			t.Fatalf("cost_usd_src = %v, want %q (P5 rama none)", te["cost_usd_src"], "none")
		}
		if got, ok := te["cost_usd"].(float64); !ok || got != 0 {
			t.Fatalf("cost_usd = %v, want 0 (dato faltante, P5)", te["cost_usd"])
		}
		if raw, present := te["cost_usd_up"]; present && raw != nil {
			t.Fatalf("cost_usd_up = %v, want null (P3)", raw)
		}
	})

	t.Run("error_lleva_model_y_none", func(t *testing.T) {
		regPath := filepath.Join(t.TempDir(), "registry.jsonl")
		h, _, _ := buildRegistry(t, []fakeUpstream{upstreamOK("m", "x")}, "sk-test-1", regPath, costRegistryOptions())

		resp := chatHello(t, h, "no-such-model")
		closeBody(t, resp)
		if resp.StatusCode != 404 {
			t.Fatalf("status = %d, want 404 (model_not_found)", resp.StatusCode)
		}

		lines := waitRegistryLines(t, regPath, 1)
		var te jsonLogLine
		for _, l := range lines {
			if l["type"] == "terminal" {
				te = l
			}
		}
		if te == nil {
			t.Fatalf("sin terminal para model_not_found:\n%s", readRegistry(t, regPath))
		}
		if te["model"] != "no-such-model" {
			t.Fatalf("terminal error model = %v, want %q (P1 aplica a error, HITL (a))", te["model"], "no-such-model")
		}
		if te["cost_usd_src"] != "none" {
			t.Fatalf("terminal error cost_usd_src = %v, want %q (P5)", te["cost_usd_src"], "none")
		}
		if raw, present := te["cost_usd_up"]; present && raw != nil {
			t.Fatalf("terminal error cost_usd_up = %v, want null (P3)", raw)
		}
	})
}

// TestPostcondition7_PrivacidadMetadataOnly verifica P7: los campos nuevos
// son strings/números de metadata y ningún contenido del request/response
// llega al archivo del registro.
func TestPostcondition7_PrivacidadMetadataOnly(t *testing.T) {
	const secret = "SECRETO-020001-999"
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageCost("m", 0.0042, true)}, "sk-test-1", regPath, costRegistryOptions())
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0}})

	resp := h.chat(t, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": secret}}})
	closeBody(t, resp)

	te := terminalSuccessOf(t, regPath)
	if _, ok := te["model"].(string); !ok {
		t.Fatalf("model = %v (%T), want string de metadata (P7)", te["model"], te["model"])
	}
	if _, ok := te["cost_usd_src"].(string); !ok {
		t.Fatalf("cost_usd_src = %v (%T), want string de metadata (P7)", te["cost_usd_src"], te["cost_usd_src"])
	}
	if raw, present := te["cost_usd_up"]; present && raw != nil {
		if _, ok := raw.(float64); !ok {
			t.Fatalf("cost_usd_up = %v (%T), want número o null (P7)", raw, raw)
		}
	}
	if content := readRegistry(t, regPath); strings.Contains(content, secret) {
		t.Fatalf("el registro filtra contenido del prompt (P7)")
	}
}

// TestPostcondition8_RespuestaByteIdentica verifica P8: la captura de
// `usage.cost` no altera el contrato observable al cliente — el envelope
// upstream se reenvía intacto (incluyendo `cost`) y el set de headers
// X-Usage-* queda congelado en los 5 conocidos (la captura no agrega
// headers ni muta la respuesta).
func TestPostcondition8_RespuestaByteIdentica(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.jsonl")
	h, _, _ := buildRegistry(t, []fakeUpstream{upstreamUsageCost("m", 0.0042, true)}, "sk-test-1", regPath, costRegistryOptions())
	h.proxySrv.SetPricing(map[string]proxy.ModelPricing{"m": {InputUSDPerM: 1.0, OutputUSDPerM: 1.0, CacheHitUSDPerM: 1.0}})

	resp := chatHello(t, h, "m")
	raw := closeBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// Passthrough intacto: el envelope (incluido usage.cost) llega al cliente.
	if !strings.Contains(string(raw), `"cost":0.0042`) {
		t.Fatalf("la captura mutó el envelope: el cliente no recibe usage.cost intacto (P8):\n%s", raw)
	}
	// Set de headers X-Usage-* congelado: exactamente los 5 conocidos.
	var got []string
	for name := range resp.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-usage-") {
			got = append(got, http.CanonicalHeaderKey(name))
		}
	}
	want := map[string]bool{
		"X-Usage-Prompt-Tokens":     true,
		"X-Usage-Completion-Tokens": true,
		"X-Usage-Total-Tokens":      true,
		"X-Usage-Cache-Hit-Tokens":  true,
		"X-Usage-Cost-Usd":          true,
	}
	if len(got) != len(want) {
		t.Fatalf("headers X-Usage-* = %v, want exactamente %v (P8: la captura no agrega headers)", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("header X-Usage-* inesperado %q (P8): %v", name, got)
		}
	}
	// Correlación captura↔envelope (P4/P8): lo capturado al terminal es
	// exactamente lo reenviado al cliente — la captura es solo-lectura.
	te := terminalSuccessOf(t, regPath)
	up, ok := te["cost_usd_up"].(float64)
	if !ok || math.Abs(up-0.0042) > 1e-12 {
		t.Fatalf("cost_usd_up terminal = %v, want 0.0042 = costo del envelope reenviado (P4/P8)", te["cost_usd_up"])
	}
}
