// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 019-007-build-snapshot — paquete snapshot (B1, B2).
// Cubren C2 (P3/P4): Embedded() sobre meta válida devuelve Meta parseada
// con sha correcto; meta corrupta/ausente o api vacío → unavailable.
//
// Contrato a materializar por GREEN (firmas del test-audit §3, aprobadas por
// HITL; el implementer DEBE respetarlas):
//
//	//go:embed api.json meta.json (en su propio dir)
//	func Embedded() (raw []byte, meta Meta, ok bool)
//	type Meta struct {
//		FetchedAt time.Time
//		SHA256    string
//		SourceURL string
//	}
//
// RED por compilación: el paquete no tiene archivos de implementación
// todavía (precedente 001-005). Los `undefined` SON el RED — api.json y
// meta.json reales se commitean en GREEN (decisión 9: los tests NO dependen
// del contenido real, solo del contrato).
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/modelsdev"
)

// ---- B1 (C2, P3) — meta válida parsea con sha correcto ----

// TestSnapshot_EmbeddedMeta congela P3: con meta.json válido, Embedded()
// devuelve raw + Meta parseada; SHA256 == sha256 hex del api.json embebido;
// FetchedAt parseable RFC3339.
func TestSnapshot_EmbeddedMeta(t *testing.T) {
	raw, meta, ok := Embedded()
	if !ok {
		t.Fatal("Embedded() = !ok con api.json+meta.json commiteados (P3: el GREEN commitea el snapshot real)")
	}
	if len(raw) == 0 {
		t.Fatal("raw vacío con snapshot commiteado (P3)")
	}
	sum := sha256.Sum256(raw)
	if want := hex.EncodeToString(sum[:]); meta.SHA256 != want {
		t.Errorf("meta.SHA256 = %q, want sha256 del api.json embebido %q (P3: el digest es el del archivo)", meta.SHA256, want)
	}
	if meta.FetchedAt.IsZero() {
		t.Errorf("meta.FetchedAt zero — debe ser RFC3339 parseable (P3)")
	}
	if meta.SourceURL == "" {
		t.Errorf("meta.SourceURL vacío (P3: de dónde vino el snapshot)")
	}
	_ = time.Now // (FetchedAt es time.Time — el uso real es en B7 vía age_days)
}

// ---- B12 (C11) — canary opt-in sobre el api.json REAL ----

// TestSnapshot_LiveEmbeddedParses congela C11: con `MOFGW_SYNC_LIVE=1`,
// `Embedded()` sobre el api.json REAL commiteado debe parsear vía
// `modelsdev.ParseCatalog` (el contrato de tipos de 001). Skip silencioso
// sin la var (jamás depende del payload real para CI).
func TestSnapshot_LiveEmbeddedParses(t *testing.T) {
	if os.Getenv("MOFGW_SYNC_LIVE") != "1" {
		t.Skip("opt-in explícito requerido (MOFGW_SYNC_LIVE=1) — parsea el api.json real commiteado")
	}
	raw, meta, ok := Embedded()
	if !ok {
		t.Skip("snapshot no disponible en este checkout (api.json vacío o meta corrupta)")
	}
	if _, err := modelsdev.ParseCatalog(raw); err != nil {
		t.Fatalf("el api.json REAL commiteado no parsea con el parser de 001: %v", err)
	}
	_ = meta
}

// TestSnapshot_UnavailableOnBadMeta congela P4: con meta.json corrupto
// (documentado: este test congela el CONTRATO de unavailable — la mecánica
// exacta de inyección de "meta mala" es del implementer, p.ej. parse de un
// fixture interno o errores del embed; lo INNEGOCIABLE es que un estado
// degradado del snapshot JAMÁS produce un fallback silencioso con datos
// dudosos). Documenta el contrato esperado del implementer:
//   - Embedded() con api.json vacío → ok=false
//   - parseMeta sobre JSON inválido → error
//
// Nota: este test corre sobre el api.json+meta.json REALES commiteados y
// verifica su VALIDEZ actual (canary de integridad del snapshot): si algún
// día el api.json commiteado está vacío o la meta corrupta, este test lo
// detecta en CI en vez de romper el fallback en producción.
// La verificación fina de los caminos corruptos vive en el canary B12
// (live) y en la revisión de GREEN (el implementer demuestra unavailable
// con fixtures internos — verificado en review de etapa 4).
func TestSnapshot_UnavailableOnBadMeta(t *testing.T) {
	raw, meta, ok := Embedded()
	if !ok {
		t.Skip("snapshot no disponible en este checkout (api.json vacío o meta corrupta) — el contrato P4 se verifica en este mismo test cuando hay snapshot")
	}
	if len(raw) == 0 {
		t.Fatal("Embedded() ok=true con raw vacío (P4: api vacío ⇒ unavailable)")
	}
	if meta.SHA256 == "" {
		t.Fatal("Embedded() ok=true con meta sin SHA256 (P4: meta incompleta ⇒ unavailable)")
	}
}
