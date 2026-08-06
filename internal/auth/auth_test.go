// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testAuthenticator() *Authenticator {
	return New([]Client{
		{ID: "openclaw", KeySHA256: HashKey("sk-ofap-secret-1")},
		{ID: "cron", KeySHA256: HashKey("sk-cron-secret-2")},
	})
}

func TestHashKeyLengthAndDeterminism(t *testing.T) {
	h1 := HashKey("sk-ofap-secret-1")
	h2 := HashKey("sk-ofap-secret-1")
	if len(h1) != 64 {
		t.Fatalf("hash len = %d, want 64", len(h1))
	}
	if h1 != h2 {
		t.Fatal("hash no determinista")
	}
	if strings.Contains(h1, "sk-ofap") {
		t.Fatal("hash contiene la key en claro")
	}
	if HashKey("sk-ofap-secret-1") == HashKey("sk-ofap-secret-2") {
		t.Fatal("keys distintas producen el mismo hash")
	}
}

func TestAuthenticateValidKey(t *testing.T) {
	a := testAuthenticator()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-ofap-secret-1")
	id, err := a.Authenticate(req)
	if err != nil {
		t.Fatalf("key válida rechazada: %v", err)
	}
	if id != "openclaw" {
		t.Fatalf("id = %q, want openclaw", id)
	}
}

func TestAuthenticateNoHeader(t *testing.T) {
	a := testAuthenticator()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if _, err := a.Authenticate(req); err != ErrInvalidKey {
		t.Fatalf("err = %v, want ErrInvalidKey", err)
	}
}

func TestAuthenticateUnknownKey(t *testing.T) {
	a := testAuthenticator()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-wrong-key")
	if _, err := a.Authenticate(req); err != ErrInvalidKey {
		t.Fatalf("err = %v, want ErrInvalidKey", err)
	}
}

func TestAuthenticateMalformedHeader(t *testing.T) {
	a := testAuthenticator()
	for _, h := range []string{"Basic abc", "Bearer", "Bearer ", "bearer sk-ofap-secret-1 extra"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("Authorization", h)
		if _, err := a.Authenticate(req); err != ErrInvalidKey {
			t.Fatalf("header %q: err = %v, want ErrInvalidKey", h, err)
		}
	}
}

func TestAuthenticateSchemeCaseInsensitive(t *testing.T) {
	a := testAuthenticator()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "bearer sk-ofap-secret-1")
	if _, err := a.Authenticate(req); err != nil {
		t.Fatalf("bearer minúscula rechazada: %v", err)
	}
}

func TestWrapRequiresAuthOnV1(t *testing.T) {
	a := testAuthenticator()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := a.Wrap(inner, "/healthz", "/metrics")

	// Sin header -> 401 invalid_api_key
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_api_key") {
		t.Fatalf("body no contiene invalid_api_key: %s", rec.Body.String())
	}

	// Key desconocida -> 401
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer nope")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	// Key válida -> pasa al handler
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-ofap-secret-1")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestWrapPublicEndpointsSkipAuth(t *testing.T) {
	a := testAuthenticator()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := a.Wrap(inner, "/healthz", "/metrics")
	for _, path := range []string{"/healthz", "/metrics"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s sin auth: status = %d, want 200", path, rec.Code)
		}
	}
}

func TestWrapOtherPublicPathPrefix(t *testing.T) {
	a := testAuthenticator()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// prefix público /healthz no debe liberar /v1/healthz
	h := a.Wrap(inner, "/healthz")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/healthz", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/healthz: status = %d, want 401 (prefix no debe filtrar)", rec.Code)
	}
}
