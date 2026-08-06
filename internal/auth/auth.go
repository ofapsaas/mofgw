// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package auth implementa la autenticación de clientes por API key
// (feature 001-007-auth). Cada cliente tiene su propia key (Bearer token);
// las keys NUNCA se guardan en claro en disco: el config referencia un
// hash SHA-256 hex (key_sha256). Los endpoints de operación (/healthz,
// /metrics) quedan sin auth para monitoreo local (bind default loopback).
//
// La comparación usa hashes de longitud fija (subtle.ConstantTimeCompare),
// de modo que el timing no depende de la longitud de la key.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// ErrInvalidKey es el error canónico para requests sin auth válida.
var ErrInvalidKey = errors.New("invalid API key")

// ctxKey es el tipo de las claves de context de auth (evita colisiones).
type ctxKey int

const clientIDKey ctxKey = 0

// ClientIDFrom devuelve el id del cliente autenticado, o "" si el
// request no pasó por Wrap (no debería pasar en rutas /v1/*).
func ClientIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(clientIDKey).(string)
	return v
}

// Authenticator valida Bearer keys contra el set de clientes conocido.
// Es seguro para uso concurrente (los clients se leen sin mutación
// después de New).
type Authenticator struct {
	byHash map[string]string // key_sha256 -> client id
}

// New construye un Authenticator desde la lista de clientes del config.
// El hash se normaliza a minúsculas; un hash malformado se rechaza en la
// validación de config (001-002), así que acá solo indexamos.
func New(clients []Client) *Authenticator {
	a := &Authenticator{byHash: make(map[string]string, len(clients))}
	for _, c := range clients {
		a.byHash[strings.ToLower(c.KeySHA256)] = c.ID
	}
	return a
}

// Client es la vista mínima que auth necesita del config (evita acoplar
// auth a config; el proxy hace el mapping).
type Client struct {
	ID        string
	KeySHA256 string
}

// HashKey devuelve el sha256 hex de una key (para `mofgw hash-key`).
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Authenticate extrae el Bearer del header Authorization y lo valida.
// Devuelve el id del cliente autenticado, o ErrInvalidKey.
func (a *Authenticator) Authenticate(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", ErrInvalidKey
	}
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", ErrInvalidKey
	}
	sum := sha256.Sum256([]byte(token))
	hexSum := hex.EncodeToString(sum[:])
	for storedHash, id := range a.byHash {
		if subtle.ConstantTimeCompare([]byte(hexSum), []byte(storedHash)) == 1 {
			return id, nil
		}
	}
	return "", ErrInvalidKey
}

// Wrap devuelve un http.Handler que exige auth válida para las rutas /v1/*
// y deja pasar sin auth las rutas públicas (healthz, metrics).
func (a *Authenticator) Wrap(next http.Handler, publicPrefixes ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range publicPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}
		clientID, err := a.Authenticate(r)
		if err != nil {
			writeOpenAIError(w, http.StatusUnauthorized, "Invalid API key", "invalid_api_key")
			return
		}
		ctx := context.WithValue(r.Context(), clientIDKey, clientID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeOpenAIError escribe un error en el formato OpenAI-compatible
// (usado también por proxy para errores de cliente).
func writeOpenAIError(w http.ResponseWriter, status int, message, typ string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// El marshal de un struct fijo no puede fallar; si fallara, no hay
	// nada razonable que hacer más allá del WriteHeader ya emitido.
	_ = writeJSON(w, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    typ,
			"code":    nil,
		},
	})
}
