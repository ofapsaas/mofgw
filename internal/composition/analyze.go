// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Análisis estructural del contexto (009-001-context-composition P1):
// Analyze recorre el body JSON crudo UNA vez y devuelve, además de
// keyPaths/detected (semántica IDÉNTICA a Walk — audit 009-000 R1), la
// composición estructural: roles de cada mensaje, part types con
// bytes/est_tokens, cantidad de tool calls y tamaño total. Walk queda
// intacto (backward compatible); Analyze es el superset. El resultado
// NUNCA incluye contenido (P7/I1): solo metadata estructural.
package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// WalkResult es el resultado de Analyze: el superset de Walk más la
// composición estructural (P1).
type WalkResult struct {
	KeyPaths   []KeyPath    // igual que Walk (dedup, orden estable)
	Detected   []DetectedID // igual que Walk (safe fields, I3)
	Roles      []string     // rol de cada mensaje, en orden
	PartTypes  []PartType   // composición por porción (ver PartType)
	NumTools   int          // cantidad total de entries de tool_calls
	TotalBytes int          // len(body)
}

// PartType es la composición de una porción de contenido: SOLO metadata
// estructural (role, type, bytes, est_tokens) — nunca contenido (I1).
type PartType struct {
	Role      string // system|user|assistant|tool|developer|other
	Type      string // text|image|tool_result|tool_use|thinking|audio|other
	Bytes     int    // longitud cruda de la porción (valor JSON, sin wrapper)
	EstTokens int    // Bytes/4 (división entera, patrón estimatePromptTokens)
}

// Analyze recorre el body JSON crudo UNA vez (P1) y devuelve un WalkResult
// con keyPaths/detected idénticos a Walk y la composición estructural:
//   - Roles: rol de cada mensaje de `messages`, en orden de aparición.
//   - PartTypes: una entrada por porción de contenido (P1): content string
//     → text; content array → una porción por elemento (text, image_url →
//     image, input_audio → audio, tool_use, tool_result, thinking/
//     redacted_thinking → thinking, otro → other); message con tool_calls
//     → una porción tool_use por entry (el JSON crudo de `arguments`);
//     message con role: tool → porción tool_result; reasoning_content no
//     vacío → porción thinking.
//   - NumTools: cantidad de entradas de tool_calls.
//   - TotalBytes: len(body).
//
// maxDepth acota la recursión (default 10); exceder trunca la rama sin
// error ni panic (I4). Devuelve error solo si el body no es JSON válido.
func Analyze(body []byte, maxDepth int) (*WalkResult, error) {
	a := &analyzer{seen: make(map[string]bool), maxDepth: maxDepth, totalBytes: len(body)}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := a.walkValue(dec, "", 1); err != nil {
		return nil, err
	}
	return &WalkResult{
		KeyPaths:   a.keyPaths,
		Detected:   a.detected,
		Roles:      a.roles,
		PartTypes:  a.partTypes,
		NumTools:   a.numTools,
		TotalBytes: a.totalBytes,
	}, nil
}

// pendingPart es una porción detectada pendiente de asignar el rol del
// mensaje (el rol puede aparecer después de content en el documento).
type pendingPart struct {
	typ   string
	bytes int
}

// analyzer mantiene el estado del recorrido único de Analyze.
type analyzer struct {
	keyPaths   []KeyPath
	detected   []DetectedID
	seen       map[string]bool
	maxDepth   int
	totalBytes int

	roles     []string
	partTypes []PartType
	numTools  int

	// estado por mensaje (objeto en messages[]): el rol se asigna a las
	// porciones al cerrar el mensaje (flushMessage).
	msgRole  string
	msgParts []pendingPart
	// estado por elemento de content[]: el tipo del elemento en curso.
	elemType       string
	elemStartParts int
}

// inMessage reporta si el objeto actual es un mensaje de `messages`.
func (a *analyzer) inMessage(prefix string) bool { return prefix == "messages[]." }

// inContentElem reporta si el objeto actual es un elemento de content[].
func (a *analyzer) inContentElem(prefix string) bool { return prefix == "messages[].content[]." }

// walkValue procesa el valor actual (el próximo token del decoder).
func (a *analyzer) walkValue(dec *json.Decoder, path string, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch d := tok.(type) {
	case json.Delim:
		switch d {
		case '{':
			return a.walkObject(dec, path, depth)
		case '[':
			return a.walkArray(dec, path, depth)
		}
		return fmt.Errorf("composition: token inesperado %q", d)
	default:
		// escalar en la raíz: sin nombre, no aporta path
		if path != "" {
			emitPath(path, &a.keyPaths, a.seen)
		}
		return nil
	}
}

// walkObject recorre las claves de un objeto en orden de aparición
// (misma lógica de paths que Walk, más la extracción estructural).
func (a *analyzer) walkObject(dec *json.Decoder, prefix string, depth int) error {
	if a.inMessage(prefix) {
		a.msgRole = ""
		a.msgParts = nil
	}
	if a.inContentElem(prefix) {
		a.elemType = ""
		a.elemStartParts = len(a.msgParts)
	}
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
				if depth >= a.maxDepth {
					emitPath(fullPath, &a.keyPaths, a.seen)
					return skipValue(dec) // truncación de la rama (I4)
				}
				if err := a.walkObject(dec, fullPath+".", depth+1); err != nil {
					return err
				}
			case '[':
				if depth >= a.maxDepth {
					emitPath(fullPath, &a.keyPaths, a.seen)
					return skipValue(dec)
				}
				if err := a.walkArray(dec, fullPath+"[]", depth+1); err != nil {
					return err
				}
			default:
				return fmt.Errorf("composition: delim inesperado %q", d)
			}
		default:
			emitPath(fullPath, &a.keyPaths, a.seen)
			if s, ok := next.(string); ok && isSafeField(fullPath, key) && isIDLike(s) {
				a.detected = append(a.detected, DetectedID{Path: fullPath, Value: s})
			}
			a.extractScalar(prefix, key, next)
		}
	}
	_, err := dec.Token() // '}' final
	if err != nil {
		return err
	}
	if a.inContentElem(prefix) {
		// elemento con type pero sin payload reconocido → other (P1)
		if a.elemType != "" && len(a.msgParts) == a.elemStartParts {
			a.msgParts = append(a.msgParts, pendingPart{typ: "other"})
		}
	}
	if a.inMessage(prefix) {
		a.flushMessage()
	}
	return nil
}

// walkArray recorre los elementos de un array; los paths de hijos se
// deduplican entre elementos (igual que Walk).
func (a *analyzer) walkArray(dec *json.Decoder, prefix string, depth int) error {
	for dec.More() {
		next, err := dec.Token()
		if err != nil {
			return err
		}
		switch d := next.(type) {
		case json.Delim:
			switch d {
			case '{':
				if err := a.walkObject(dec, prefix+".", depth); err != nil {
					return err
				}
			case '[':
				if err := a.walkArray(dec, prefix, depth); err != nil {
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

// extractScalar extrae la composición estructural de un valor escalar
// (P1): roles, content, reasoning_content, elementos de content[] y
// entries de tool_calls. NUNCA guarda el valor en el resultado (I1).
func (a *analyzer) extractScalar(prefix, key string, value any) {
	switch {
	case a.inMessage(prefix) && key == "role":
		if s, ok := value.(string); ok {
			a.msgRole = s
			a.roles = append(a.roles, s)
		}
	case a.inMessage(prefix) && key == "content":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "text", bytes: len(s)})
		}
	case a.inMessage(prefix) && key == "reasoning_content":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "thinking", bytes: len(s)})
		}
	case a.inContentElem(prefix) && key == "type":
		if s, ok := value.(string); ok {
			a.elemType = s
		}
	case a.inContentElem(prefix) && key == "text" && a.elemType == "text":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "text", bytes: len(s)})
		}
	case prefix == "messages[].content[].image_url." && key == "url" && a.elemType == "image_url":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "image", bytes: len(s)})
		}
	case prefix == "messages[].content[].input_audio." && key == "data" && a.elemType == "input_audio":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "audio", bytes: len(s)})
		}
	case a.inContentElem(prefix) && key == "thinking" && a.elemType == "thinking":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "thinking", bytes: len(s)})
		}
	case a.inContentElem(prefix) && key == "redacted_thinking" && a.elemType == "redacted_thinking":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "thinking", bytes: len(s)})
		}
	case a.inContentElem(prefix) && key == "input" && a.elemType == "tool_use":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "tool_use", bytes: len(s)})
		}
	case a.inContentElem(prefix) && key == "content" && a.elemType == "tool_result":
		if s, ok := value.(string); ok {
			a.msgParts = append(a.msgParts, pendingPart{typ: "tool_result", bytes: len(s)})
		}
	case strings.HasPrefix(prefix, "messages[].tool_calls[].") && key == "arguments":
		if s, ok := value.(string); ok {
			raw, err := json.Marshal(s)
			if err == nil {
				a.numTools++
				a.msgParts = append(a.msgParts, pendingPart{typ: "tool_use", bytes: len(raw)})
			}
		}
	}
}

// flushMessage cierra el mensaje actual: asigna el rol a las porciones
// bufferizadas y las agrega al resultado (P1). El rol ausente cae en
// "other"; el content string de un mensaje role: tool es tool_result.
func (a *analyzer) flushMessage() {
	for _, p := range a.msgParts {
		role := a.msgRole
		if role == "" {
			role = "other"
		}
		typ := p.typ
		if typ == "text" && role == "tool" {
			typ = "tool_result"
		}
		a.partTypes = append(a.partTypes, PartType{Role: role, Type: typ, Bytes: p.bytes, EstTokens: p.bytes / 4})
	}
	a.msgRole = ""
	a.msgParts = nil
}
