package absorb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ofapsaas/mofgw/internal/provider"
	"github.com/ofapsaas/mofgw/internal/router"
)

// secrets que NUNCA deben aparecer en mensajes al cliente.
const (
	secretURL  = "http://10.0.0.5:8080/internal/debug?token=abc"
	secretKey  = "sk-ultrasecreto-123456"
	secretIP   = "192.168.1.99"
	secretPath = "/etc/webmin/virtualmin-prod/config"
)

func TestSanitizeNil(t *testing.T) {
	if got := Sanitize(nil); got != nil {
		t.Fatalf("Sanitize(nil) = %v, want nil", got)
	}
}

func TestSanitizeAlreadyAbsorbed(t *testing.T) {
	ae := &AbsorbedError{StatusCode: 502, Code: "upstream_unavailable", Message: "x", Type: "upstream_error"}
	if got := Sanitize(ae); got != ae {
		t.Fatal("errores ya absorbidos deben pasar directo")
	}
}

func TestSanitizeChain5xxTo502(t *testing.T) {
	ce := &router.ChainError{
		Status: 500, Type: "upstream_error", Code: "upstream_error",
		Message: "boom 500: " + secretURL + " " + secretKey,
		Err:     errors.New("upstream"),
	}
	ae := Sanitize(ce)
	if ae.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want 502", ae.StatusCode)
	}
	if ae.Code != "upstream_unavailable" {
		t.Fatalf("Code = %q, want upstream_unavailable", ae.Code)
	}
	if ae.Message != "upstream provider error" {
		t.Fatalf("Message = %q, want genérico (sin detalles internos)", ae.Message)
	}
	assertNoLeaks(t, ae.Message)
}

func TestSanitizeChain429To502(t *testing.T) {
	ce := &router.ChainError{
		Status: http.StatusTooManyRequests, Type: "upstream_error", Code: "upstream_error",
		Message: "rate limited", Err: errors.New("429"),
	}
	ae := Sanitize(ce)
	if ae.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want 502 (429 agotado → 502)", ae.StatusCode)
	}
}

func TestSanitizePreservesDegradationCodes(t *testing.T) {
	// 002-004: all_providers_down y degraded_limit son códigos semánticos
	// para el operador — absorb NO debe reescribirlos a upstream_unavailable.
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"all_providers_down", http.StatusBadGateway},
		{"degraded_limit", http.StatusServiceUnavailable},
	} {
		ce := &router.ChainError{
			Status: tc.status, Type: "upstream_error", Code: tc.code,
			Message: "no providers available", Err: errors.New("down"),
		}
		ae := Sanitize(ce)
		if ae.StatusCode != tc.status {
			t.Fatalf("%s: StatusCode = %d, want %d", tc.code, ae.StatusCode, tc.status)
		}
		if ae.Code != tc.code {
			t.Fatalf("%s: Code = %q, want %q (código semántico conservado)", tc.code, ae.Code, tc.code)
		}
		if ae.Message != "upstream provider error" {
			t.Fatalf("%s: Message = %q, want genérico (sin detalles internos)", tc.code, ae.Message)
		}
	}
}

func TestSanitizeChainClient4xxPassthrough(t *testing.T) {
	ce := &router.ChainError{
		Status: http.StatusNotFound, Type: "model_not_found", Code: "model_not_found",
		Message: `no provider serves model "gpt-x"`, Err: nil,
	}
	ae := Sanitize(ce)
	if ae.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want 404 (4xx de cliente pasa con su status)", ae.StatusCode)
	}
	if ae.Code != "model_not_found" {
		t.Fatalf("Code = %q, want model_not_found", ae.Code)
	}
}

func TestSanitizeProvider401To502(t *testing.T) {
	ce := &router.ChainError{
		Status: http.StatusUnauthorized, Type: "authentication_error", Code: "authentication_error",
		Message: "invalid api key: " + secretKey, ProviderID: "provider-a", Err: errors.New("401"),
	}
	ae := Sanitize(ce)
	if ae.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want 502 (401 de provider = config del proxy)", ae.StatusCode)
	}
	if ae.Code != "provider_auth_error" {
		t.Fatalf("Code = %q, want provider_auth_error", ae.Code)
	}
	if strings.Contains(ae.Message, secretKey) {
		t.Fatal("la key no debe viajar al cliente")
	}
}

func TestSanitizeProvider403To502(t *testing.T) {
	ce := &router.ChainError{
		Status: http.StatusForbidden, Type: "authentication_error", Code: "authentication_error",
		Message: "forbidden", ProviderID: "p2", Err: errors.New("403"),
	}
	if got := Sanitize(ce).StatusCode; got != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want 502 (403 de provider)", got)
	}
}

func TestSanitizeRawUpstream(t *testing.T) {
	ue := &provider.ErrUpstream{
		StatusCode: 500, Type: "upstream_error",
		Message: "internal: " + secretPath,
	}
	ae := Sanitize(ue)
	if ae.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want 502", ae.StatusCode)
	}
	if ae.Message != "upstream provider error" {
		t.Fatalf("Message = %q, want genérico", ae.Message)
	}
	assertNoLeaks(t, ae.Message)
}

func TestSanitizeRawUpstreamAuth(t *testing.T) {
	ue := &provider.ErrUpstream{StatusCode: 401, Type: "authentication_error", Message: secretKey}
	ae := Sanitize(ue)
	if ae.Code != "provider_auth_error" {
		t.Fatalf("Code = %q, want provider_auth_error", ae.Code)
	}
	assertNoLeaks(t, ae.Message)
}

func TestSanitizeInternalError(t *testing.T) {
	ae := Sanitize(errors.New("panic in handler: " + secretPath))
	if ae.StatusCode != http.StatusInternalServerError {
		t.Fatalf("StatusCode = %d, want 500", ae.StatusCode)
	}
	if ae.Code != "internal_error" {
		t.Fatalf("Code = %q, want internal_error", ae.Code)
	}
	assertNoLeaks(t, ae.Message)
}

func TestSanitizeKeepsErrForLogs(t *testing.T) {
	orig := errors.New("detail only for logs: " + secretURL)
	ae := Sanitize(orig)
	if ae.Err == nil {
		t.Fatal("Err debe conservar el original (para logs del proxy)")
	}
	if !errors.Is(ae, orig) {
		t.Fatal("Unwrap debe permitir errors.Is sobre el original")
	}
}

func TestRespondWritesOpenAIJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	Respond(rec, &router.ChainError{
		Status: 502, Type: "upstream_error", Code: "upstream_unavailable",
		Message: "all providers failed", Err: errors.New("up"),
	})

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body no es JSON válido: %v", err)
	}
	if body.Error.Type != "upstream_error" || body.Error.Code != "upstream_unavailable" {
		t.Fatalf("body = %+v, want upstream_error/upstream_unavailable", body.Error)
	}
}

func TestRespondClientErrorStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	Respond(rec, ClientError(http.StatusBadRequest, "max_tokens too big", "invalid_request_error"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestErrAllProvidersDownSentinel(t *testing.T) {
	if ErrAllProvidersDown.StatusCode != http.StatusBadGateway {
		t.Fatal("sentinel debe ser 502")
	}
	if ErrAllProvidersDown.Code != "all_providers_down" {
		t.Fatal("sentinel code = all_providers_down")
	}
}

// assertNoLeaks verifica que el mensaje no contenga URLs internas, keys,
// IPs privadas ni paths de archivos.
func assertNoLeaks(t *testing.T, msg string) {
	t.Helper()
	for _, secret := range []string{secretURL, secretKey, secretIP, secretPath, "http://", "Bearer", "sk-"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("mensaje filtra secreto %q: %q", secret, msg)
		}
	}
}
