// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package claude_test

// Tests RED de la feature 013-002 (adapter claude.Backend). Mapean a las
// postcondiciones P1–P11 y a los criterios C1–C11 del spec aprobado (tabla
// §4.2, 19 tests). Compilan contra el esqueleto de compilación de
// internal/subprocess/claude/claude.go y fallan por ASSERTION (el esqueleto
// devuelve ""/nil/false/error).

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/subprocess"
	"github.com/ofapsaas/mofgw/internal/subprocess/claude"
)

// TestT_Name — P1 / C2: Name() == "claude".
// Esqueleto devuelve "" ⇒ RED.
func TestT_Name(t *testing.T) {
	adapter := newTestAdapter(t)
	if got := adapter.Name(); got != "claude" {
		t.Fatalf("Name() = %q, want %q", got, "claude")
	}
}

// TestT_ArgvBuild — P2 / C3: argv exacto en orden.
// Esqueleto devuelve nil ⇒ RED.
func TestT_ArgvBuild(t *testing.T) {
	bin := buildClaudeStubCLI(t)
	adapter := claude.New(bin)
	sess := &subprocess.Session{ID: "sess-1", Dir: t.TempDir(), ClientID: "client-a", New: true}

	got := adapter.Args(sess, "claude-sonnet-4-6", []string{"--bare"})
	want := []string{
		bin,
		"-p",
		"-n", "client-a", // sesión nombrada: crea (New) con el clientID saneado
		"--model", "claude-sonnet-4-6",
		"--output-format", "stream-json",
		"--verbose", // requerido con --print + --output-format=stream-json (verificado CLI real)
		"--bare",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args = %v, want %v", got, want)
	}
}

// TestT_ArgvOrderFlags — P2 / C3: flags de stream-json después de --model,
// backendFlags opacos después de --model, posición consistente.
// Esqueleto devuelve nil ⇒ RED.
func TestT_ArgvOrderFlags(t *testing.T) {
	bin := buildClaudeStubCLI(t)
	adapter := claude.New(bin)
	sess := &subprocess.Session{ID: "sess-1", New: true}

	got := adapter.Args(sess, "claude-sonnet-4-6", []string{"--verbose", "-n", "3"})
	if len(got) == 0 {
		t.Fatalf("Args returned empty argv")
	}
	modelIdx, streamIdx, firstFlagIdx := -1, -1, -1
	for i, a := range got {
		switch a {
		case "--model":
			modelIdx = i
		case "--output-format":
			streamIdx = i
		case "--verbose":
			if firstFlagIdx < 0 {
				firstFlagIdx = i
			}
		}
	}
	if modelIdx < 0 {
		t.Fatalf("argv missing --model: %v", got)
	}
	if streamIdx <= modelIdx {
		t.Fatalf("--output-format must come after --model: %v", got)
	}
	if firstFlagIdx <= modelIdx || firstFlagIdx <= streamIdx {
		t.Fatalf("backend flags must come after --model and stream-json: %v", got)
	}
}

// TestT_ArgvNoPromptLeak — P3 / C4: el argv NO contiene el prompt ni content
// ni tools. Esqueleto devuelve nil ⇒ RED (falla en el check de argv no vacío).
func TestT_ArgvNoPromptLeak(t *testing.T) {
	bin := buildClaudeStubCLI(t)
	adapter := claude.New(bin)
	sess := &subprocess.Session{ID: "sess-1"}

	got := adapter.Args(sess, "claude-sonnet-4-6", []string{"--verbose"})
	if len(got) == 0 {
		t.Fatalf("Args returned empty argv")
	}
	joined := strings.Join(got, " ")
	for _, sub := range []string{`"tools"`, `"content"`, `tool_calls`} {
		if strings.Contains(joined, sub) {
			t.Fatalf("argv leaks %q: %v", sub, got)
		}
	}
}

// TestT_TranslateReqLastUser — P4 / C5: solo el ÚLTIMO mensaje user.
// Esqueleto siempre error ⇒ RED.
func TestT_TranslateReqLastUser(t *testing.T) {
	adapter := newTestAdapter(t)
	body := chatClaudeBody("claude-sonnet-4-6",
		map[string]any{"role": "system", "content": "sys"},
		map[string]any{"role": "user", "content": "first"},
		map[string]any{"role": "assistant", "content": "assistant reply"},
		map[string]any{"role": "user", "content": "LAST USER"},
	)
	got, err := adapter.TranslateReq(body)
	if err != nil {
		t.Fatalf("TranslateReq returned error: %v", err)
	}
	if got != "LAST USER" {
		t.Fatalf("TranslateReq = %q, want %q", got, "LAST USER")
	}
}

// TestT_TranslateReqTextArray — P4 / C5: content array concatena las partes
// text en orden, descartando no-texto. Esqueleto siempre error ⇒ RED.
func TestT_TranslateReqTextArray(t *testing.T) {
	adapter := newTestAdapter(t)
	body := chatClaudeBody("claude-sonnet-4-6",
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "Hello"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "x"}},
			map[string]any{"type": "text", "text": " world"},
		}},
	)
	got, err := adapter.TranslateReq(body)
	if err != nil {
		t.Fatalf("TranslateReq returned error: %v", err)
	}
	if got != "Hello world" {
		t.Fatalf("TranslateReq = %q, want %q", got, "Hello world")
	}
}

// TestT_TranslateReqNoUserError — P4 / C5: un body sin user da error (pasa
// en el esqueleto), PERO un body con user debe devolver el contenido (falla
// en el esqueleto) ⇒ la función queda RED por la aserción positiva.
func TestT_TranslateReqNoUserError(t *testing.T) {
	adapter := newTestAdapter(t)

	// Sin mensaje user ⇒ error (pasa en el esqueleto).
	bodyNoUser := chatClaudeBody("claude-sonnet-4-6",
		map[string]any{"role": "system", "content": "sys"},
		map[string]any{"role": "assistant", "content": "hi"},
	)
	if _, err := adapter.TranslateReq(bodyNoUser); err == nil {
		t.Fatalf("TranslateReq of body without user should error")
	}

	// Con user ⇒ devuelve el contenido (RED en el esqueleto, siempre error).
	bodyUser := chatClaudeBody("claude-sonnet-4-6",
		map[string]any{"role": "user", "content": "hello"},
	)
	got, err := adapter.TranslateReq(bodyUser)
	if err != nil {
		t.Fatalf("TranslateReq with user returned error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("TranslateReq = %q, want %q", got, "hello")
	}
}

// TestT_TranslateReqCleanNoTools — P5 / C6: salida sin tools, tool results,
// tool_calls ni estructura de orquestación. Esqueleto siempre error ⇒ RED.
func TestT_TranslateReqCleanNoTools(t *testing.T) {
	adapter := newTestAdapter(t)
	body, _ := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6",
		"messages": []any{
			map[string]any{"role": "user", "content": "what is 2+2"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "calc", "arguments": `{"expr":"2+2"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "4"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "calc", "parameters": map[string]any{}}},
		},
	})
	got, err := adapter.TranslateReq(body)
	if err != nil {
		t.Fatalf("TranslateReq returned error: %v", err)
	}
	if got != "what is 2+2" {
		t.Fatalf("TranslateReq = %q, want %q", got, "what is 2+2")
	}
	for _, marker := range []string{"tool_calls", "tool", "call_1", `"tools"`, `"function"`} {
		if strings.Contains(got, marker) {
			t.Fatalf("TranslateReq leaks %q: %q", marker, got)
		}
	}
}

// TestT_TranslateOutTextContent — P6 / C7: stream-json agregado → ChatResponse
// con content "Hello world", role assistant, model dado.
// Esqueleto siempre error ⇒ RED.
func TestT_TranslateOutTextContent(t *testing.T) {
	adapter := newTestAdapter(t)
	raw := claudeStreamOut("end_turn")

	resp, err := adapter.TranslateOut(raw, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("TranslateOut returned error: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatalf("TranslateOut returned no choices")
	}
	c := resp.Choices[0]
	if c.Message.Content != "Hello world" {
		t.Fatalf("content = %q, want %q", c.Message.Content, "Hello world")
	}
	if c.Message.Role != "assistant" {
		t.Fatalf("role = %q, want %q", c.Message.Role, "assistant")
	}
	if resp.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want %q", resp.Model, "claude-sonnet-4-6")
	}
}

// TestT_TranslateOutStopReasonMap — P6 / C7: mapeo stop_reason→finish_reason.
// Esqueleto siempre error ⇒ RED.
func TestT_TranslateOutStopReasonMap(t *testing.T) {
	adapter := newTestAdapter(t)
	cases := []struct {
		stop string
		want string
	}{
		{"max_tokens", "length"},
		{"end_turn", "stop"},
		{"stop_sequence", "stop"},
	}
	for _, c := range cases {
		resp, err := adapter.TranslateOut(claudeStreamOut(c.stop), "m")
		if err != nil {
			t.Fatalf("stop=%s: TranslateOut error: %v", c.stop, err)
		}
		if len(resp.Choices) == 0 {
			t.Fatalf("stop=%s: no choices", c.stop)
		}
		if resp.Choices[0].FinishReason != c.want {
			t.Fatalf("stop=%s: finish_reason = %q, want %q", c.stop, resp.Choices[0].FinishReason, c.want)
		}
	}
}

// TestT_TranslateOutInvalidJSON — P6 / C7: stdout no parseable da error (pasa
// en el esqueleto), PERO el stdout válido debe parsear y devolver contenido
// (falla en el esqueleto) ⇒ la función queda RED por la aserción positiva.
func TestT_TranslateOutInvalidJSON(t *testing.T) {
	adapter := newTestAdapter(t)

	// No parseable ⇒ error (pasa en el esqueleto).
	if _, err := adapter.TranslateOut([]byte("not json at all"), "m"); err == nil {
		t.Fatalf("TranslateOut of invalid json should error")
	}

	// Válido ⇒ devuelve contenido (RED en el esqueleto, siempre error).
	resp, err := adapter.TranslateOut(claudeStreamOut("end_turn"), "m")
	if err != nil {
		t.Fatalf("TranslateOut of valid output returned error: %v", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Hello world" {
		t.Fatalf("unexpected valid-output parse: %+v", resp)
	}
}

// TestT_TranslateStreamTextDeltas — P7 / C8: un evento Data por cada
// text_delta con delta.content. Esqueleto no emite nada ⇒ RED.
func TestT_TranslateStreamTextDeltas(t *testing.T) {
	adapter := newTestAdapter(t)
	lines := make(chan string, 16)
	for _, l := range claudeStreamLines("end_turn") {
		lines <- l
	}
	close(lines)

	ch := make(chan provider.StreamEvent, 16)
	adapter.TranslateStreamOut(context.Background(), lines, ch, "claude-sonnet-4-6")
	close(ch)

	var got []string
	for _, ev := range collectEvents(ch) {
		if ev.Err != nil {
			t.Fatalf("unexpected err event: %v", ev.Err)
		}
		got = append(got, deltaContent(ev.Data))
	}
	want := []string{"Hello", " world"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deltas = %v, want %v", got, want)
	}
}

// TestT_TranslateStreamSkipControl — P8 / C8: solo los text deltas producen
// eventos; control events y thinking/tool_use deltas NO. Esqueleto no emite
// nada ⇒ RED.
func TestT_TranslateStreamSkipControl(t *testing.T) {
	adapter := newTestAdapter(t)
	linesList := []string{
		`{"type":"message_start","message":{"id":"msg_01","role":"assistant","content":[],"stop_reason":null}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"internal reasoning"}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"t1","name":"calc","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"x\":1}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
	lines := make(chan string, len(linesList))
	for _, l := range linesList {
		lines <- l
	}
	close(lines)

	ch := make(chan provider.StreamEvent, 16)
	adapter.TranslateStreamOut(context.Background(), lines, ch, "m")
	close(ch)

	var got []string
	for _, ev := range collectEvents(ch) {
		if ev.Err != nil {
			t.Fatalf("unexpected err event: %v", ev.Err)
		}
		got = append(got, deltaContent(ev.Data))
	}
	if len(got) != 1 || got[0] != "Hi" {
		t.Fatalf("expected exactly one text delta 'Hi', got %v", got)
	}
}

// TestT_TranslateStreamNoUsageDone — P7 / C9: el stream no emite chunks con
// usage ni el literal [DONE]. Esqueleto no emite nada, así que además se
// exige que SÍ haya eventos de datos (RED) y que ninguno sea usage/[DONE].
func TestT_TranslateStreamNoUsageDone(t *testing.T) {
	adapter := newTestAdapter(t)
	lines := make(chan string, 16)
	for _, l := range claudeStreamLines("end_turn") {
		lines <- l
	}
	close(lines)

	ch := make(chan provider.StreamEvent, 16)
	adapter.TranslateStreamOut(context.Background(), lines, ch, "m")
	close(ch)

	var payloads []string
	for _, ev := range collectEvents(ch) {
		if ev.Err != nil {
			t.Fatalf("unexpected err event: %v", ev.Err)
		}
		payloads = append(payloads, ev.Data)
	}
	if len(payloads) == 0 {
		t.Fatalf("expected data events, got none")
	}
	for _, p := range payloads {
		if strings.Contains(p, "usage") {
			t.Fatalf("stream must not emit usage chunk: %s", p)
		}
		if p == "[DONE]" {
			t.Fatalf("stream must not emit [DONE]")
		}
	}
}

// TestT_TranslateStreamEmpty — P7 / C8: stream vacío no emite nada (pasa en
// el esqueleto), PERO un stream con un text_delta debe emitir UN evento (RED
// en el esqueleto) ⇒ la función queda RED por la aserción positiva.
func TestT_TranslateStreamEmpty(t *testing.T) {
	adapter := newTestAdapter(t)

	// Vacío ⇒ nada (pasa en el esqueleto).
	lines := make(chan string)
	close(lines)
	ch := make(chan provider.StreamEvent, 4)
	adapter.TranslateStreamOut(context.Background(), lines, ch, "m")
	close(ch)
	if evs := collectEvents(ch); len(evs) != 0 {
		t.Fatalf("empty stream should emit nothing, got %d events", len(evs))
	}

	// Un text_delta ⇒ UN evento (RED en el esqueleto).
	lines2 := make(chan string, 2)
	lines2 <- `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`
	close(lines2)
	ch2 := make(chan provider.StreamEvent, 4)
	adapter.TranslateStreamOut(context.Background(), lines2, ch2, "m")
	close(ch2)
	if evs := collectEvents(ch2); len(evs) != 1 {
		t.Fatalf("expected 1 data event, got %d", len(evs))
	}
}

// TestT_IsRefusal — P9 / C10 (subtests): markers de refusal ⇒ true; texto
// inocuo ⇒ false. Esqueleto devuelve false ⇒ el subtest "true" queda RED.
func TestT_IsRefusal(t *testing.T) {
	adapter := newTestAdapter(t)

	t.Run("true", func(t *testing.T) {
		if !adapter.IsRefusal("refused due to responsible use policy") {
			t.Fatalf("IsRefusal('...responsible use policy...') = false, want true")
		}
	})
	t.Run("false", func(t *testing.T) {
		if adapter.IsRefusal("server started") {
			t.Fatalf("IsRefusal('server started') = true, want false")
		}
	})
}

// TestT_ImplementsBackend — P10 / C11: assert de compilación y runtime de
// que claude.Backend implementa subprocess.Backend. Guard (compila y pasa);
// el RED de la suite viene de los tests de comportamiento.
func TestT_ImplementsBackend(t *testing.T) {
	var _ subprocess.Backend = (*claude.Backend)(nil)
	if got := subprocess.Backend(claude.New("bin")).Name(); got == "" {
		t.Logf("guard: Backend.Name() returns %q (skeleton)", got)
	}
}

// TestT_AdapterCompleteStubCli — P6,P10,P11 / C7,C11 (integración): a través
// de subprocess.NewProvider + claude.New(stubBin), Complete devuelve la
// respuesta traducida. El motor es real (013-001); el adapter es esqueleto
// ⇒ TranslateReq/TranslateOut fallan ⇒ Complete devuelve error ⇒ RED.
func TestT_AdapterCompleteStubCli(t *testing.T) {
	p := newIntegratedProvider(t)
	body := chatClaudeBody("claude-sonnet-4-6",
		map[string]any{"role": "user", "content": "hello"},
	)
	res, err := p.Complete(clientCtx("client-a"), body)
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if len(res.Response.Choices) == 0 {
		t.Fatalf("Complete returned no choices")
	}
	if got := res.Response.Choices[0].Message.Content; got != "Hello world" {
		t.Fatalf("Complete content = %q, want %q", got, "Hello world")
	}
}

// TestT_AdapterStreamStubCli — P7,P10,P11 / C8,C11 (integración): a través de
// subprocess.NewProvider + claude.New(stubBin), Stream emite los text deltas.
// El adapter es esqueleto ⇒ TranslateStreamOut no emite nada (y TranslateReq
// falla antes) ⇒ RED.
func TestT_AdapterStreamStubCli(t *testing.T) {
	p := newIntegratedProvider(t)
	body := chatClaudeBody("claude-sonnet-4-6",
		map[string]any{"role": "user", "content": "hello"},
	)
	ch, err := p.Stream(clientCtx("client-a"), body)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	var content []string
	for _, ev := range collectEvents(ch) {
		if ev.Err != nil {
			t.Fatalf("stream err event: %v", ev.Err)
		}
		if d := deltaContent(ev.Data); d != "" {
			content = append(content, d)
		}
	}
	if len(content) == 0 {
		t.Fatalf("expected text deltas from stream, got none")
	}
	if !reflect.DeepEqual(content, []string{"Hello", " world"}) {
		t.Fatalf("stream deltas = %v, want %v", content, []string{"Hello", " world"})
	}
}

// T_isrefusal_no_generic_false_positive (P8, C8): IsRefusal NO marca como
// negativa un error de sistema genérico ("cannot open file"), y SÍ marca un
// texto de policy específico ("refused due to responsible use policy"). El
// skeleton conserva el marker "cannot" ⇒ el caso genérico da true ⇒ RED.
func TestT_IsRefusalNoGenericFalsePositive(t *testing.T) {
	adapter := newTestAdapter(t)

	if adapter.IsRefusal("cannot open file") {
		t.Fatalf("IsRefusal('cannot open file') = true, want false (falso positivo genérico)") // RED
	}
	if !adapter.IsRefusal("refused due to responsible use policy") {
		t.Fatalf("IsRefusal('refused due to responsible use policy') = false, want true")
	}
}

// T_adapter_send_ctx_guarded (P4, C5): un TranslateStreamOut con ctx ya
// cancelado y canal sin consumidor retorna en un plazo acotado (no bloquea).
// El skeleton (claude.go) deja el send `ch <-` sin guard ⇒ bloquea ⇒ RED.
func TestT_AdapterSendCtxGuarded(t *testing.T) {
	adapter := newTestAdapter(t)

	lines := make(chan string, 1)
	lines <- `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`
	close(lines)
	ch := make(chan provider.StreamEvent) // sin buffer, sin consumidor
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ctx ya cancelado

	done := make(chan struct{})
	go func() {
		adapter.TranslateStreamOut(ctx, lines, ch, "m")
		close(done)
	}()
	select {
	case <-done:
		// retornó sin bloquear ✓
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("TranslateStreamOut con ctx cancelado y canal sin consumidor NO retorna (bloquea)") // RED
	}
}

// T_session_named — regresión del smoke real (12 Ago): el CLI claude usa
// sesiones NOMBRADAS. Sesión nueva → `-n <nombre>` (crea); subsiguiente →
// `--resume <nombre>` (continúa, reutiliza cache). El nombre = clientID
// saneado a [a-zA-Z0-9_-]. Si el clientID es vacío, cae al session key
// sha256 hex del motor.
func TestT_SessionNamed(t *testing.T) {
	bin := buildClaudeStubCLI(t)
	adapter := claude.New(bin)

	// Nueva → -n <clientID saneado>.
	argv := adapter.Args(&subprocess.Session{ID: "ignored", ClientID: "agent-main", New: true}, "m", nil)
	if !hasArgPair(argv, "-n", "agent-main") {
		t.Fatalf("argv de sesión nueva debe ser -n <clientID>: %v", argv)
	}
	if hasArg(argv, "--resume") {
		t.Fatalf("argv de sesión nueva no debe tener --resume: %v", argv)
	}

	// Subsiguiente → --resume <clientID saneado>.
	argvR := adapter.Args(&subprocess.Session{ID: "ignored", ClientID: "agent-main", New: false}, "m", nil)
	if !hasArgPair(argvR, "--resume", "agent-main") {
		t.Fatalf("argv de sesión subsiguiente debe ser --resume <clientID>: %v", argvR)
	}

	// Sanitización: chars no seguros → guiones, trims de extremos.
	argvS := adapter.Args(&subprocess.Session{ID: "ignored", ClientID: "go.corp/1!", New: true}, "m", nil)
	if !hasArgPair(argvS, "-n", "go-1") {
		t.Fatalf("clientID saneado = %v, want go-1", argvS)
	}

	// ClientID vacío → fallback al session key (sha256 hex del motor).
	argvF := adapter.Args(&subprocess.Session{ID: "e0b107f9f96f69a2b6165a2ac7ae5516", ClientID: "", New: true}, "m", nil)
	if !hasArgPair(argvF, "-n", "e0b107f9f96f69a2b6165a2ac7ae5516") {
		t.Fatalf("clientID vacío debe caer al session key: %v", argvF)
	}

	// El argv incluye --verbose (requerido con --print + stream-json).
	for _, a := range argv {
		if a == "--verbose" {
			return
		}
	}
	t.Fatalf("argv sin --verbose (requerido para stream-json con --print): %v", argv)
}

// hasArgPair reporta si argv contiene el par (flag, value) consecutivo.
func hasArgPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

// hasArg reporta si argv contiene el flag.
func hasArg(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}
