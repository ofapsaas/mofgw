// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package composition analiza la estructura de un body JSON crudo para la
// telemetría de descubrimiento (009-000): key paths en notación de dots y
// detección de ids en campos seguros. El body viaja CRUDO ([]byte — el
// proxy es transparente al contenido) y el walk no re-marshal ni pierde
// campos: solo LEE.
package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// KeyPath es un path de clave en notación de dots, e.g. "messages[].role".
type KeyPath string

// DetectedID es un valor id-like hallado en un campo seguro (P7).
type DetectedID struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

// Walk recorre un body JSON crudo y devuelve:
//   - keyPaths: paths de TODAS las claves con notación de dots
//     ("model", "messages[].role", "metadata.session_id"). Orden ESTABLE:
//     orden de aparición en el documento (primera ocurrencia),
//     deduplicado. Los arrays se anotan "name[]" y se recorre cada
//     elemento (paths de hijos deduplicados entre elementos). Sin valores.
//   - detected: valores id-like (prefijos ses_, msg_, agent:) hallados
//     SOLO en campos seguros: metadata.*, *.session_id, *.thread_id,
//     role, name, tool_call_id (P7/I3). Nunca inspecciona content, text,
//     description, input, arguments (solo aportan su path).
//   - err: solo si body no es JSON válido.
//
// maxDepth acota la recursión (default 10); exceder la profundidad trunca
// esa rama sin error ni panic (I4).
func Walk(body []byte, maxDepth int) (keyPaths []KeyPath, detected []DetectedID, err error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	seen := make(map[string]bool)
	if err := walkValue(dec, "", 1, maxDepth, &keyPaths, &detected, seen); err != nil {
		return nil, nil, err
	}
	return keyPaths, detected, nil
}

// walkValue procesa el valor actual (el próximo token del decoder).
func walkValue(dec *json.Decoder, path string, depth, maxDepth int, keyPaths *[]KeyPath, detected *[]DetectedID, seen map[string]bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch d := tok.(type) {
	case json.Delim:
		switch d {
		case '{':
			return walkObject(dec, path, depth, maxDepth, keyPaths, detected, seen)
		case '[':
			return walkArray(dec, path, depth, maxDepth, keyPaths, detected, seen)
		}
		return fmt.Errorf("composition: token inesperado %q", d)
	default:
		// escalar en la raíz: sin nombre, no aporta path
		if path != "" {
			emitPath(path, keyPaths, seen)
		}
		return nil
	}
}

// walkObject recorre las claves de un objeto en orden de aparición.
func walkObject(dec *json.Decoder, prefix string, depth, maxDepth int, keyPaths *[]KeyPath, detected *[]DetectedID, seen map[string]bool) error {
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("composition: clave de objeto no string")
		}
		fullPath := prefix + key

		// peek del valor: consume el delim de apertura si es contenedor.
		next, err := dec.Token()
		if err != nil {
			return err
		}
		switch d := next.(type) {
		case json.Delim:
			switch d {
			case '{':
				if depth >= maxDepth {
					emitPath(fullPath, keyPaths, seen)
					return skipValue(dec) // truncación de la rama (I4)
				}
				if err := walkObject(dec, fullPath+".", depth+1, maxDepth, keyPaths, detected, seen); err != nil {
					return err
				}
			case '[':
				if depth >= maxDepth {
					emitPath(fullPath, keyPaths, seen)
					return skipValue(dec)
				}
				if err := walkArray(dec, fullPath+"[]", depth+1, maxDepth, keyPaths, detected, seen); err != nil {
					return err
				}
			default:
				return fmt.Errorf("composition: delim inesperado %q", d)
			}
		default:
			emitPath(fullPath, keyPaths, seen)
			if s, ok := next.(string); ok && isSafeField(fullPath, key) && isIDLike(s) {
				*detected = append(*detected, DetectedID{Path: fullPath, Value: s})
			}
		}
	}
	_, err := dec.Token() // '}' final
	return err
}

// walkArray recorre los elementos de un array; los paths de hijos se
// deduplican entre elementos (primera ocurrencia, orden estable).
func walkArray(dec *json.Decoder, prefix string, depth, maxDepth int, keyPaths *[]KeyPath, detected *[]DetectedID, seen map[string]bool) error {
	for dec.More() {
		next, err := dec.Token()
		if err != nil {
			return err
		}
		switch d := next.(type) {
		case json.Delim:
			switch d {
			case '{':
				// Las claves del objeto interno van después del "[]" con un
				// separador de punto: "messages[].role". prefix ya termina
				// en "[]" (o es un path de objeto previo); agregar el punto.
				if err := walkObject(dec, prefix+".", depth, maxDepth, keyPaths, detected, seen); err != nil {
					return err
				}
			case '[':
				if err := walkArray(dec, prefix, depth, maxDepth, keyPaths, detected, seen); err != nil {
					return err
				}
			default:
				return fmt.Errorf("composition: delim inesperado %q", d)
			}
		default:
			// escalar suelto en array: sin nombre, no aporta path ni detección
		}
	}
	_, err := dec.Token() // ']' final
	return err
}

// skipValue consume el resto del contenedor cuyo delim de apertura ya se
// leyó (truncación por maxDepth).
func skipValue(dec *json.Decoder) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// emitPath agrega un path una única vez (primera aparición).
func emitPath(path string, keyPaths *[]KeyPath, seen map[string]bool) {
	if seen[path] {
		return
	}
	seen[path] = true
	*keyPaths = append(*keyPaths, KeyPath(path))
}

// isSafeField decide si un campo puede inspeccionarse como valor (P7/I3):
// metadata.*, *.session_id, *.thread_id, role, name, tool_call_id.
func isSafeField(path, key string) bool {
	return strings.HasPrefix(path, "metadata.") ||
		key == "session_id" || key == "thread_id" ||
		key == "role" || key == "name" || key == "tool_call_id"
}

// isIDLike decide si un valor es id-like (prefijos ses_, msg_, agent:).
func isIDLike(v string) bool {
	return strings.HasPrefix(v, "ses_") || strings.HasPrefix(v, "msg_") || strings.HasPrefix(v, "agent:")
}
