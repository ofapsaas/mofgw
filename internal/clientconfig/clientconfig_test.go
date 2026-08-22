// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 016-001-config-renderer-core — contrato PÚBLICO
// del paquete clientconfig (registry + IR).
//
// La feature 016-001 entrega el CORE del paquete sin adapters: el registry
// ESTÁ VACÍO en producción, pero registrar/consultar renderers y transportar
// la metadata del catálogo ES la API pública del paquete (spec P10). Estos
// tests ejercitan ese contrato directamente — sin mocks de plumbing — a la
// espera de la implementación del paquete en GREEN.
//
// Contrato que estas pruebas exigen (RED, will-not-compile):
//   - tipo `ConfigIR` con campos `ClientID`, `BaseURL`, `KeyEnvRef`, `Models`
//   - variable exportada `Renderers map[string]Renderer`
//   - funciones `Register(clientID string, r Renderer)`,
//     `SupportedList() []string`, `Lookup(clientID string) Renderer`

package clientconfig

import "testing"

// stubRenderer implementa Renderer para ejercitar el path 200 del endpoint
// a través de la API pública (P5/P10). Devuelve un blob JSON trivial.
type stubRenderer struct{ body []byte }

func (s stubRenderer) Render(ir ConfigIR) ([]byte, error) {
	if len(s.body) == 0 {
		return []byte(`{}`), nil
	}
	out := make([]byte, len(s.body))
	copy(out, s.body)
	return out, nil
}

// resetRegistry deja el registry en su estado inicial (vacío), limpiando
// cualquier registro de otro test. Opera SOLO sobre el mapa exportado
// (público), como prescribe P10.
func resetRegistry(t *testing.T) {
	t.Helper()
	for id := range Renderers {
		delete(Renderers, id)
	}
	t.Cleanup(func() {
		for id := range Renderers {
			delete(Renderers, id)
		}
	})
}

// TestRED_RegisterSupportedListLookup_P10 — P10: Register/SupportedList/Lookup
// son la API pública del paquete y operan sobre el mapa exportado `Renderers`.
// Registrar/consultar NO es un mock: es el contrato del registry.
func TestRED_RegisterSupportedListLookup_P10(t *testing.T) {
	resetRegistry(t)

	if got := len(SupportedList()); got != 0 {
		t.Fatalf("SupportedList() recién reseteado = %d, want 0 (registry vacío)", got)
	}

	Register("a", stubRenderer{})
	Register("b", stubRenderer{})

	// SupportedList refleja el contenido del registry.
	if got := len(SupportedList()); got != 2 {
		t.Fatalf("SupportedList() = %d, want 2 (a y b registrados)", got)
	}

	// Lookup devuelve el renderer registrado; nil para unknown.
	if Lookup("a") == nil {
		t.Fatal("Lookup(a) = nil, want el renderer registrado")
	}
	if Lookup("b") == nil {
		t.Fatal("Lookup(b) = nil, want el renderer registrado")
	}
	if Lookup("missing") != nil {
		t.Fatal("Lookup(missing) != nil, want nil para cliente no registrado")
	}
}

// TestRED_RenderersMapExportadoSePuebla — P10: Register escribe en el mapa
// exportado `Renderers` (inspectable/registrable desde la API pública), y
// Lookup lee del mismo mapa. Garantiza que el path 200/404 es testeable sin
// fixtures externos (decisión D4).
func TestRED_RenderersMapExportadoSePuebla_P10(t *testing.T) {
	resetRegistry(t)

	Register("x", stubRenderer{body: []byte(`{"client":"opencode"}`)})

	if _, ok := Renderers["x"]; !ok {
		t.Fatalf("Register(x) no pobló el mapa exportado Renderers: %v", Renderers)
	}
	if Lookup("x") == nil {
		t.Fatalf("Lookup(x) != nil esperado tras Register(x)")
	}

	// Escribir directamente en el mapa exportado también debe ser
	// consultable por Lookup (el mapa ES la fuente del registry, no una copia).
	Renderers["direct"] = stubRenderer{}
	if Lookup("direct") == nil {
		t.Fatal("Lookup(direct) = nil, want el renderer escrito directo en el mapa exportado")
	}
	if got := len(SupportedList()); got != 2 {
		t.Fatalf("SupportedList() = %d, want 2 (x y direct)", got)
	}
}

// TestRED_ConfigIRKeyEnvRefNuncaValor — P8/I1: el IR transporta `KeyEnvRef`
// (la referencia a la variable de entorno), NUNCA el valor del secret.
// Compila contra el tipo `ConfigIR` y exige su campo `KeyEnvRef`.
func TestRED_ConfigIRKeyEnvRefNuncaValor_P10(t *testing.T) {
	secret := "sk-literal-secret-987"
	ir := ConfigIR{
		ClientID:  "test",
		BaseURL:   "https://mofgw.example/v1",
		KeyEnvRef: "MOFGW_KEY", // referencia, no el valor
	}

	if ir.KeyEnvRef != "MOFGW_KEY" {
		t.Fatalf("ConfigIR.KeyEnvRef = %q, want la referencia MOFGW_KEY", ir.KeyEnvRef)
	}
	if ir.KeyEnvRef == secret {
		t.Fatalf("ConfigIR.KeyEnvRef no debe contener el valor del secret: %q", ir.KeyEnvRef)
	}
	if ir.ClientID != "test" {
		t.Fatalf("ConfigIR.ClientID = %q, want test", ir.ClientID)
	}
	if ir.BaseURL != "https://mofgw.example/v1" {
		t.Fatalf("ConfigIR.BaseURL = %q", ir.BaseURL)
	}
}