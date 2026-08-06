// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeUpstream responde /chat/completions con el comportamiento pedido.
func fakeUpstream(t *testing.T, status int, body string, stream bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		io.WriteString(w, body)
	}))
}

func testClient(t *testing.T, base string) *Client {
	t.Helper()
	return NewClient("fake1", base, "sk-upstream", []string{"gpt-4o-mini"}, 4096, nil)
}

func chatBody(model string) []byte {
	b, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	return b
}

func TestCompleteOK(t *testing.T) {
	srv := fakeUpstream(t, 200, `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hola"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`, false)
	defer srv.Close()
	c := testClient(t, srv.URL)

	res, err := c.Complete(context.Background(), chatBody("gpt-4o-mini"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Response.Choices[0].Message.Content != "hola" {
		t.Fatalf("content = %q, want hola", res.Response.Choices[0].Message.Content)
	}
	if res.Response.Usage.TotalTokens != 7 {
		t.Fatalf("total_tokens = %d, want 7", res.Response.Usage.TotalTokens)
	}
	// el body crudo se preserva tal cual (el cliente ve el upstream)
	if !strings.Contains(string(res.Raw), "chatcmpl-1") {
		t.Fatalf("raw no preservado: %s", res.Raw)
	}
}

func TestCompleteUpstream500(t *testing.T) {
	srv := fakeUpstream(t, 500, `{"error":{"message":"boom","type":"server_error"}}`, false)
	defer srv.Close()
	c := testClient(t, srv.URL)

	_, err := c.Complete(context.Background(), chatBody("gpt-4o-mini"))
	var ue *ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *ErrUpstream", err)
	}
	if ue.StatusCode != 500 || ue.Type != "upstream_error" {
		t.Fatalf("status/type = %d/%s, want 500/upstream_error", ue.StatusCode, ue.Type)
	}
	if strings.Contains(ue.Message, "sk-upstream") {
		t.Fatal("mensaje de error filtra la key")
	}
}

func TestCompleteUpstream400(t *testing.T) {
	srv := fakeUpstream(t, 400, `{"error":{"message":"bad request","type":"invalid_request_error"}}`, false)
	defer srv.Close()
	c := testClient(t, srv.URL)

	_, err := c.Complete(context.Background(), chatBody("gpt-4o-mini"))
	var ue *ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *ErrUpstream", err)
	}
	if ue.Type != "invalid_request_error" {
		t.Fatalf("type = %s, want invalid_request_error", ue.Type)
	}
}

func TestCompleteNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := testClient(t, url)

	_, err := c.Complete(context.Background(), chatBody("gpt-4o-mini"))
	var ue *ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *ErrUpstream", err)
	}
	if ue.Type != "network" {
		t.Fatalf("type = %s, want network", ue.Type)
	}
}

func TestStreamOK(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"hol\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: [DONE]\n\n"
	srv := fakeUpstream(t, 200, sse, true)
	defer srv.Close()
	c := testClient(t, srv.URL)

	ch, err := c.Stream(context.Background(), chatBody("gpt-4o-mini"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var datas []string
	for ev := range ch {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		datas = append(datas, ev.Data)
	}
	if len(datas) != 3 || datas[2] != "[DONE]" {
		t.Fatalf("datas = %v, want 3 eventos con [DONE] final", datas)
	}
}

func TestStreamUpstream500BeforeFirstByte(t *testing.T) {
	srv := fakeUpstream(t, 500, `{"error":{"message":"down","type":"server_error"}}`, true)
	defer srv.Close()
	c := testClient(t, srv.URL)

	_, err := c.Stream(context.Background(), chatBody("gpt-4o-mini"))
	var ue *ErrUpstream
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *ErrUpstream", err)
	}
	if ue.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", ue.StatusCode)
	}
}

func TestRawBodyPreserved(t *testing.T) {
	// el body crudo con campos no-tipados (tools, top_p) debe llegar
	// intacto al upstream
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(200)
		io.WriteString(w, `{"id":"x","object":"chat.completion","model":"m","choices":[]}`)
	}))
	defer srv.Close()
	c := testClient(t, srv.URL)

	body := []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}],"top_p":0.5,"stream_options":{"include_usage":true}}`)
	if _, err := c.Complete(context.Background(), body); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(gotBody, `"tools"`) || !strings.Contains(gotBody, `"top_p"`) || !strings.Contains(gotBody, `"stream_options"`) {
		t.Fatalf("campos no-tipados perdidos en el round-trip: %s", gotBody)
	}
}

func TestParseChatRequest(t *testing.T) {
	req, err := ParseChatRequest([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`))
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}
	if req.Model != "m" || req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Fatalf("req = %+v", req)
	}
	if _, err := ParseChatRequest([]byte(`{"messages":[]}`)); err == nil {
		t.Fatal("sin model debería fallar")
	}
	if _, err := ParseChatRequest([]byte(`{"model":"m"}`)); err == nil {
		t.Fatal("sin messages debería fallar")
	}
	if _, err := ParseChatRequest([]byte(`not json`)); err == nil {
		t.Fatal("body inválido debería fallar")
	}
}

func TestParseChatRequestContentArray(t *testing.T) {
	// Producción: OpenClaw envía messages[].content como array de partes
	// (formato reasoning: [{"type":"text","text":"hola"}]). El parseo de
	// ruteo solo decide model/stream/max_tokens y cuenta messages — nunca
	// debe rechazar un request válido por la forma del contenido.
	req, err := ParseChatRequest([]byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hola"}]}]}`))
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}
	if req.Model != "m" {
		t.Fatalf("req.Model = %q, want %q", req.Model, "m")
	}
}

func TestParseChatRequestContentNull(t *testing.T) {
	// Postcondición del spec: content null también debe aceptarse sin error.
	req, err := ParseChatRequest([]byte(`{"model":"m","messages":[{"role":"user","content":null}]}`))
	if err != nil {
		t.Fatalf("ParseChatRequest: %v", err)
	}
	if req.Model != "m" {
		t.Fatalf("req.Model = %q, want %q", req.Model, "m")
	}
}

func TestServesAndModels(t *testing.T) {
	c := testClient(t, "http://x")
	if !c.Serves("gpt-4o-mini") {
		t.Fatal("Serves(gpt-4o-mini) = false")
	}
	if c.Serves("gpt-5") {
		t.Fatal("Serves(gpt-5) = true, want false")
	}
	ms := c.Models()
	if len(ms) != 1 || ms[0] != "gpt-4o-mini" {
		t.Fatalf("Models = %v", ms)
	}
	if c.MaxTokens() != 4096 {
		t.Fatalf("MaxTokens = %d, want 4096", c.MaxTokens())
	}
}

func TestSanitizeRedactsURLsAndKeys(t *testing.T) {
	got := sanitize("error connecting to https://api.secret.io/v1 with Bearer sk-abc123")
	if strings.Contains(got, "https://") || strings.Contains(got, "Bearer") || strings.Contains(got, "sk-abc") {
		t.Fatalf("sanitize no redactó: %q", got)
	}
}
