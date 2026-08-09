// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests RED de la feature 009-002-sticky-session — P3/P4/P5 (C2-C8, C12).
// Verifican AffinityStore (set/get/upsert/touch/evicción LRU con fake
// clock, cap default, Len <= cap) y el reordenamiento post-filtro de
// CompleteFor/StreamFor: preferido primero preservando el resto, no-aplica
// (cooldown/health/Serves/clave ausente/clave vacía), sesiones
// independientes, 404 model_not_found con afinidad, fallback
// pre-primer-byte con preferencia y sin mezcla post-primer-byte.
//
// RED por compilación (patrón 009-001): AffinityStore, NewAffinityStore,
// Options.StickyMaxEntries, Affinity(), CompleteFor y StreamFor aún no
// existen. Firmas que fijan (spec §Contrato — firmas):
//
//	type AffinityStore struct { ... }
//	func NewAffinityStore(now func() time.Time, cap int) *AffinityStore
//	func (s *AffinityStore) Set(key, providerID string)
//	func (s *AffinityStore) Get(key string) (string, bool)
//	func (s *AffinityStore) Len() int
//	func (r *Router) Affinity() *AffinityStore
//	func (r *Router) CompleteFor(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*provider.CompleteResult, error)
//	func (r *Router) StreamFor(ctx context.Context, req *provider.ChatRequest, body []byte, stickyKey string) (*StreamResult, error)
//	Options.StickyMaxEntries int

package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/ofapsaas/mofgw/internal/provider"
)

// ---- P3 — AffinityStore ----

func TestPostcondition3_AffinityStore_SetGetUpsert(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	s := NewAffinityStore(clock, 10)

	if got, ok := s.Get("test|s1"); ok || got != "" {
		t.Fatalf("Get ausente = (%q,%v), want (\"\",false)", got, ok)
	}
	s.Set("test|s1", "p1")
	got, ok := s.Get("test|s1")
	if !ok || got != "p1" {
		t.Fatalf("Get = (%q,%v), want (p1,true)", got, ok)
	}
	// Upsert: reemplaza y refresca LastUsed (P3).
	s.Set("test|s1", "p2")
	got, ok = s.Get("test|s1")
	if !ok || got != "p2" {
		t.Fatalf("Get tras upsert = (%q,%v), want (p2,true)", got, ok)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

func TestPostcondition3_AffinityStore_LRUEviction(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	s := NewAffinityStore(clock, 2)

	s.Set("a", "p1")
	now = now.Add(time.Second)
	s.Set("b", "p2")
	now = now.Add(time.Second)
	s.Set("c", "p3") // excede cap → evicta "a" (LastUsed más viejo)
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (nunca excede cap — C12)", s.Len())
	}
	if got, ok := s.Get("a"); ok || got != "" {
		t.Fatalf("Get(a) = (%q,%v), want (\"\",false) — evictada LRU (P3)", got, ok)
	}
	if got, ok := s.Get("c"); !ok || got != "p3" {
		t.Fatalf("Get(c) = (%q,%v), want (p3,true)", got, ok)
	}
}

func TestPostcondition3_AffinityStore_GetRefrescaLastUsed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	s := NewAffinityStore(clock, 2)

	s.Set("a", "p1")
	now = now.Add(time.Second)
	s.Set("b", "p2")
	now = now.Add(time.Second)
	if _, ok := s.Get("a"); !ok {
		t.Fatal("Get(a) = false, want true")
	}
	now = now.Add(time.Second)
	s.Set("c", "p3") // excede cap → evicta "b" ("a" fue touched)
	if got, ok := s.Get("b"); ok || got != "" {
		t.Fatalf("Get(b) = (%q,%v), want (\"\",false) — b es la menos reciente (P3)", got, ok)
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatal("Get(a) = false, want true (LastUsed refrescado por Get)")
	}
	if _, ok := s.Get("c"); !ok {
		t.Fatal("Get(c) = false, want true")
	}
}

func TestPostcondition3_AffinityStore_ClaveActivaNoSeEvicta(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	s := NewAffinityStore(clock, 2)

	s.Set("test|", "p1") // cliente sin sesión (C7)
	now = now.Add(time.Second)
	s.Set("test|sA", "p1")
	now = now.Add(time.Second)
	s.Set("test|", "p1") // touch: la clave client| sigue activa
	now = now.Add(time.Second)
	s.Set("test|sB", "p1") // excede cap → evicta test|sA (la más vieja)
	if got, ok := s.Get("test|sA"); ok || got != "" {
		t.Fatalf("Get(test|sA) = (%q,%v), want evictada", got, ok)
	}
	if _, ok := s.Get("test|"); !ok {
		t.Fatal("Get(test|) = false, want true (clave client| activa NO se evicta — C12)")
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}

func TestPostcondition3_AffinityStore_CapDefaultCien(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	s := NewAffinityStore(clock, 0) // cap 0 = default 100 (P3)
	for i := 0; i < 150; i++ {
		s.Set(fmt.Sprintf("k%d", i), "p1")
		now = now.Add(time.Nanosecond)
	}
	if s.Len() != 100 {
		t.Fatalf("Len = %d, want 100 (cap default — P3/I6)", s.Len())
	}
}

func TestPostcondition3_AffinityAccessorEnRouter(t *testing.T) {
	r := NewWithOptions(specsOf(newFake("p1", "m")), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 5,
	})
	if r.Affinity() == nil {
		t.Fatal("Affinity() = nil, want *AffinityStore (P3)")
	}
	if r.Affinity().Len() != 0 {
		t.Fatalf("Affinity().Len() = %d, want 0 (arranque frío — I5)", r.Affinity().Len())
	}
	r.Affinity().Set("k", "p1")
	if got, ok := r.Affinity().Get("k"); !ok || got != "p1" {
		t.Fatalf("Get vía accessor = (%q,%v), want (p1,true)", got, ok)
	}
}

// ---- P4 — CompleteFor: reordenamiento post-filtro ----

func TestPostcondition4_PreferidoPrimero(t *testing.T) {
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p2") // preferido: p2

	res, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("CompleteFor: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (preferido primero — C2)", res.ProviderID)
	}
	if p2.callCount() != 1 || p1.callCount() != 0 {
		t.Fatalf("calls = p1:%d p2:%d, want 0/1 (1 solo intento, sin fallback — C2)", p1.callCount(), p2.callCount())
	}
}

func TestPostcondition4_SinAfinidadOrdenConfig(t *testing.T) {
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})

	// Clave ausente/evictada → sin afinidad → orden de config (p1 primero).
	res, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|sX")
	if err != nil {
		t.Fatalf("CompleteFor: %v", err)
	}
	if res.ProviderID != "p1" {
		t.Fatalf("provider = %s, want p1 (sin afinidad, orden de config)", res.ProviderID)
	}

	// stickyKey vacío → comportamiento IDÉNTICO a legacy (P4/P8).
	res, err = r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "")
	if err != nil {
		t.Fatalf("CompleteFor clave vacía: %v", err)
	}
	if res.ProviderID != "p1" {
		t.Fatalf("provider con clave vacía = %s, want p1 (P4)", res.ProviderID)
	}
}

func TestPostcondition4_PreferidoEnCooldownNoAplica(t *testing.T) {
	now := time.Now()
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Cooldowns().now = func() time.Time { return now }
	r.Affinity().Set("test|s1", "p1") // preferido: p1
	r.Cooldowns().Set("p1", time.Minute)

	res, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("CompleteFor: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (preferido en cooldown no aplica — C3)", res.ProviderID)
	}
	if p1.callCount() != 0 {
		t.Fatalf("p1 intentos = %d, want 0 (en cooldown, no se prueba — I2)", p1.callCount())
	}
}

func TestPostcondition4_PreferidoUnhealthyNoAplica(t *testing.T) {
	hs := newHealthStore(t, map[string]error{"p1": errors.New("down"), "p2": nil})
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Health: hs, StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p1")

	res, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("CompleteFor: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (preferido unhealthy no aplica — C4)", res.ProviderID)
	}
	if p1.callCount() != 0 {
		t.Fatalf("p1 intentos = %d, want 0 (salteado por health — I2)", p1.callCount())
	}
}

func TestPostcondition4_PreferidoNoSirveModeloNoAplica(t *testing.T) {
	p1 := newFake("p1", "m1").ok("de p1") // p1 NO sirve "m"
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p1")

	res, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("CompleteFor: %v", err)
	}
	if res.ProviderID != "p2" {
		t.Fatalf("provider = %s, want p2 (preferido no sirve el modelo — C5)", res.ProviderID)
	}
	if p1.callCount() != 0 {
		t.Fatalf("p1 intentos = %d, want 0 (no sirve el modelo — I2)", p1.callCount())
	}
}

func TestPostcondition4_ModelNotFoundConAfinidad(t *testing.T) {
	p1 := newFake("p1", "m1")
	r := NewWithOptions(specsOf(p1), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p1")

	_, err := r.CompleteFor(context.Background(), reqFor("nope"), bodyFor("nope", nil), "test|s1")
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *ChainError", err)
	}
	if ce.Status != http.StatusNotFound || ce.Type != "model_not_found" {
		t.Fatalf("status/type = %d/%s, want 404/model_not_found (P4)", ce.Status, ce.Type)
	}
}

func TestPostcondition4_SesionesIndependientes(t *testing.T) {
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 0, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p1")
	r.Affinity().Set("test|s2", "p2")

	res1, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("CompleteFor s1: %v", err)
	}
	if res1.ProviderID != "p1" {
		t.Fatalf("s1 provider = %s, want p1", res1.ProviderID)
	}
	res2, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s2")
	if err != nil {
		t.Fatalf("CompleteFor s2: %v", err)
	}
	if res2.ProviderID != "p2" {
		t.Fatalf("s2 provider = %s, want p2", res2.ProviderID)
	}
	if p2.callCount() != 0 {
		t.Fatalf("p2 intentos = %d, want 0 (s1 no usa el preferido de s2 — C6)", p2.callCount())
	}
	if p1.callCount() != 0 {
		t.Fatalf("p1 intentos = %d, want 0 (s2 no usa el preferido de s1 — C6)", p1.callCount())
	}
}

func TestPostcondition4_StickyAppliedLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p1 := newFake("p1", "m").ok("de p1")
	p2 := newFake("p2", "m").ok("de p2")
	r := NewWithOptions(specsOf(p1, p2), Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		Logger: logger, StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p2")

	if _, err := r.CompleteFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1"); err != nil {
		t.Fatalf("CompleteFor: %v", err)
	}
	lines := parseLogLines(t, buf.String())
	if got := linesWithMsg(lines, "sticky_applied"); len(got) != 1 {
		t.Fatalf("sticky_applied = %d líneas, want 1 (D6/C10):\n%s", len(got), buf.String())
	}
}

// ---- P5 — StreamFor: reorder + frontera del primer byte ----

func TestPostcondition5_PreferidoPrimeroStream(t *testing.T) {
	p1 := &streamFake{fakeProvider: newFake("p1", "m").ok("x"), script: []provider.StreamEvent{{Data: "de p1"}, {Data: "[DONE]"}}}
	p2 := &streamFake{fakeProvider: newFake("p2", "m").ok("x"), script: []provider.StreamEvent{{Data: "de p2"}, {Data: "[DONE]"}}}
	r := NewWithOptions([]ProviderSpec{{Provider: p1}, {Provider: p2}}, Options{
		MaxRetries: 0, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p2")

	res, err := r.StreamFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("StreamFor: %v", err)
	}
	if res.Provider.ID() != "p2" {
		t.Fatalf("provider = %s, want p2 (preferido primero — P5)", res.Provider.ID())
	}
	if p1.callCount() != 0 {
		t.Fatalf("p1 intentos = %d, want 0", p1.callCount())
	}
	var datas []string
	for ev := range res.Events {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		datas = append(datas, ev.Data)
	}
	if len(datas) != 2 || datas[0] != "de p2" || datas[1] != "[DONE]" {
		t.Fatalf("datas = %v, want [de p2, [DONE]] (stream de UN solo provider — P5)", datas)
	}
}

func TestPostcondition5_FallbackPrePrimerByteConPreferencia(t *testing.T) {
	// Preferido p2 devuelve 429 antes del primer byte → fallback a p1
	// (el reorder solo cambia el orden de prueba — P5/C8).
	p2 := newFake("p2", "m").fail429()
	p1 := &streamFake{fakeProvider: newFake("p1", "m").ok("x"), script: []provider.StreamEvent{{Data: "hola"}, {Data: "[DONE]"}}}
	r := NewWithOptions([]ProviderSpec{{Provider: p1}, {Provider: p2}}, Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p2")

	res, err := r.StreamFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("StreamFor: %v", err)
	}
	if res.Provider.ID() != "p1" {
		t.Fatalf("provider = %s, want p1 (fallback pre-primer-byte del preferido — C8)", res.Provider.ID())
	}
	if p2.callCount() != 1 {
		t.Fatalf("p2 intentos = %d, want 1 (el preferido se prueba primero)", p2.callCount())
	}
	var datas []string
	for ev := range res.Events {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		datas = append(datas, ev.Data)
	}
	if len(datas) != 2 || datas[0] != "hola" || datas[1] != "[DONE]" {
		t.Fatalf("datas = %v, want [hola, [DONE]]", datas)
	}
}

func TestPostcondition5_SinMezclaPostPrimerByte(t *testing.T) {
	// p2 arranca (primer byte) y muere a mitad: el error viaja por el
	// canal; NO reconecta a p1 (regla 001-003 §B, sin cambios — P5/C8).
	p2 := &streamFake{fakeProvider: newFake("p2", "m").ok("x"), script: []provider.StreamEvent{
		{Data: `{"choices":[{"delta":{"content":"ho"}}]}`},
		{Err: errors.New("stream murió a mitad")},
	}}
	p1 := &streamFake{fakeProvider: newFake("p1", "m").ok("x"), script: []provider.StreamEvent{{Data: "nunca"}, {Data: "[DONE]"}}}
	r := NewWithOptions([]ProviderSpec{{Provider: p1}, {Provider: p2}}, Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})
	r.Affinity().Set("test|s1", "p2")

	res, err := r.StreamFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|s1")
	if err != nil {
		t.Fatalf("StreamFor: %v", err)
	}
	if res.Provider.ID() != "p2" {
		t.Fatalf("provider = %s, want p2 (pinneado al provider que arrancó — P5)", res.Provider.ID())
	}
	if p1.callCount() != 0 {
		t.Fatalf("p1 intentos = %d, want 0 (sin reconexión post-primer-byte)", p1.callCount())
	}
	var events []provider.StreamEvent
	for ev := range res.Events {
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("eventos = %d, want 2 (primer byte + error)", len(events))
	}
	if events[0].Err != nil || events[1].Err == nil {
		t.Fatalf("eventos = %+v, want [data, error]", events)
	}
}

func TestPostcondition5_SinAfinidadFallbackLegacy(t *testing.T) {
	p1 := &streamFake{fakeProvider: newFake("p1", "m")} // script nil → 500 pre-primer-byte
	p2 := &streamFake{fakeProvider: newFake("p2", "m").ok("x"), script: []provider.StreamEvent{{Data: "hola"}, {Data: "[DONE]"}}}
	r := NewWithOptions([]ProviderSpec{{Provider: p1}, {Provider: p2}}, Options{
		MaxRetries: 1, Cooldown: time.Minute, GlobalTimeout: 30 * time.Second,
		StickyMaxEntries: 10,
	})

	res, err := r.StreamFor(context.Background(), reqFor("m"), bodyFor("m", nil), "test|sX")
	if err != nil {
		t.Fatalf("StreamFor: %v", err)
	}
	if res.Provider.ID() != "p2" {
		t.Fatalf("provider = %s, want p2 (fallback pre-primer-byte sin afinidad — P4/P8)", res.Provider.ID())
	}
}
