// Package clamp reescribe max_tokens/max_completion_tokens de un request
// OpenAI-compatible al límite del provider destino (feature 001-004).
//
// Es un paquete puro: sin I/O, sin estado. El clamp es por-request y
// nunca muta la config. Un valor ausente o 0 no se toca; un provider con
// max_tokens=0 (sin límite configurado) no clampea.
package clamp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Request clampa los campos de tokens del body JSON al límite providerMax.
// Devuelve el body reescrito. Si providerMax <= 0 no aplica ningún clamp
// (solo valida el JSON). Los campos no relacionados se preservan intactos.
func Request(body []byte, providerMax int64) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("clamp: body inválido: %w", err)
	}
	if providerMax <= 0 {
		return body, nil
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		raw, ok := m[field]
		if !ok {
			continue
		}
		var v int64
		if err := json.Unmarshal(raw, &v); err != nil {
			// campo no numérico: lo dejamos pasar, el upstream decidirá
			continue
		}
		if v > providerMax {
			clamped, err := json.Marshal(providerMax)
			if err != nil {
				return nil, fmt.Errorf("clamp: marshal %s: %w", field, err)
			}
			m[field] = clamped
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("clamp: re-encode: %w", err)
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}
