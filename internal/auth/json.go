package auth

import (
	"encoding/json"
	"io"
)

// writeJSON serializa v al ResponseWriter (helper compartido para errores
// OpenAI-compatible sin duplicar el encoder).
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
