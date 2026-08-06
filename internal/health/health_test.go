package health

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeChecker es un checker programable sin red.
type fakeChecker struct {
	id   string
	mu   sync.Mutex
	err  error
	n    int
	last int // último valor de n visto por el checker (para detectar re-checks)
}

func (f *fakeChecker) ID() string { return f.id }

func (f *fakeChecker) Health(ctx context.Context) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	f.last = f.n
	if f.err != nil {
		return 0, f.err
	}
	return 3 * time.Millisecond, nil
}

func (f *fakeChecker) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeChecker) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// httpChecker es un checker real contra un httptest.Server.
type httpChecker struct {
	id  string
	url string
}

func (h *httpChecker) ID() string { return h.id }

func (h *httpChecker) Health(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url+"/models", nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return time.Since(start), err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return time.Since(start), errors.New("health check status " + http.StatusText(resp.StatusCode))
	}
	return time.Since(start), nil
}

func TestCheckNowHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	st := New(time.Second, 5*time.Second, discardLogger())
	st.Register(&httpChecker{id: "p1", url: srv.URL})
	st.CheckNow()

	if !st.IsHealthy("p1") {
		t.Fatal("p1 healthy=true esperado tras check OK")
	}
	snap := st.Snapshot()
	s := snap["p1"]
	if s.LastCheck.IsZero() {
		t.Fatal("LastCheck no debería ser zero tras un check")
	}
	if s.LastLatency <= 0 {
		t.Fatalf("LastLatency = %v, want > 0", s.LastLatency)
	}
	if s.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0", s.ConsecutiveFailures)
	}
}

func TestCheckNowUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	st := New(time.Second, 5*time.Second, discardLogger())
	st.Register(&httpChecker{id: "p1", url: srv.URL})
	st.CheckNow()

	if st.IsHealthy("p1") {
		t.Fatal("p1 healthy=false esperado tras check 500")
	}
	if got := st.Snapshot()["p1"].ConsecutiveFailures; got != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1", got)
	}
}

func TestRecovery(t *testing.T) {
	var code int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
	defer srv.Close()

	st := New(time.Second, 5*time.Second, discardLogger())
	st.Register(&httpChecker{id: "p1", url: srv.URL})

	code = http.StatusServiceUnavailable
	st.CheckNow()
	if st.IsHealthy("p1") {
		t.Fatal("p1 debería estar unhealthy con 503")
	}

	code = http.StatusOK // el upstream se recuperó
	st.CheckNow()
	if !st.IsHealthy("p1") {
		t.Fatal("p1 debería volver a healthy tras recuperarse (500→200)")
	}
	if got := st.Snapshot()["p1"].ConsecutiveFailures; got != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0 tras recuperación", got)
	}
}

func TestUnknownProviderIsHealthyColdStart(t *testing.T) {
	st := New(time.Second, 5*time.Second, discardLogger())
	// Nunca registrado ni sondeado: el router no debe saltearlo.
	if !st.IsHealthy("nunca-visto") {
		t.Fatal("provider desconocido debe ser healthy (cold start)")
	}
}

func TestCheckDueRespectsInterval(t *testing.T) {
	st := New(time.Hour, 5*time.Second, discardLogger()) // intervalo grande
	fc := &fakeChecker{id: "p1"}
	st.Register(fc)

	st.CheckNow()
	if got := fc.count(); got != 1 {
		t.Fatalf("calls = %d, want 1 tras CheckNow", got)
	}
	// CheckDue inmediato: el intervalo (1h) no venció → no re-sondea.
	st.CheckDue()
	if got := fc.count(); got != 1 {
		t.Fatalf("calls = %d, want 1 (CheckDue no debe re-chequear antes del intervalo)", got)
	}
}

func TestCheckDueAfterIntervalElapsed(t *testing.T) {
	// now inyectable para avanzar el reloj sin esperar.
	now := time.Now()
	st := New(10*time.Millisecond, 5*time.Second, discardLogger())
	st.now = func() time.Time { return now }
	fc := &fakeChecker{id: "p1"}
	st.Register(fc)

	st.CheckNow() // lastCheck = now
	if got := fc.count(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	// Avanzar 20ms → el intervalo (10ms) venció → CheckDue re-sondea.
	now = now.Add(20 * time.Millisecond)
	st.CheckDue()
	if got := fc.count(); got != 2 {
		t.Fatalf("calls = %d, want 2 tras vencer el intervalo", got)
	}
}

func TestStartCancelsCleanly(t *testing.T) {
	st := New(10*time.Millisecond, 5*time.Second, discardLogger())
	st.Register(&fakeChecker{id: "p1"})

	ctx, cancel := context.WithCancel(context.Background())
	done := st.Start(ctx)
	cancel()

	select {
	case <-done:
		// goroutine terminó limpiamente
	case <-time.After(time.Second):
		t.Fatal("el goroutine de health no terminó en < 1s tras cancel")
	}
}

func TestSnapshotIsolatedFromMutations(t *testing.T) {
	st := New(time.Second, 5*time.Second, discardLogger())
	fc := &fakeChecker{id: "p1"}
	st.Register(fc)
	st.CheckNow()

	// Mutar la copia devuelta no debe afectar el store.
	snap := st.Snapshot()
	s := snap["p1"]
	s.ConsecutiveFailures = 99
	s.Healthy = false

	if got := st.Snapshot()["p1"].ConsecutiveFailures; got != 0 {
		t.Fatalf("mutar la snapshot no debe afectar el store, ConsecutiveFailures = %d", got)
	}
	if !st.IsHealthy("p1") {
		t.Fatal("mutar la snapshot no debe afectar healthy del store")
	}
}

func TestRegisterReplacesResetsState(t *testing.T) {
	st := New(time.Second, 5*time.Second, discardLogger())
	fc := &fakeChecker{id: "p1", err: errors.New("down")}
	st.Register(fc)
	st.CheckNow()
	if st.IsHealthy("p1") {
		t.Fatal("p1 unhealthy esperado")
	}
	// Re-registrar el mismo id con un checker sano: estado reseteado.
	st.Register(&fakeChecker{id: "p1"})
	if got := st.Snapshot()["p1"].ConsecutiveFailures; got != 0 {
		t.Fatalf("re-register debe resetear estado, ConsecutiveFailures = %d", got)
	}
}

func TestLen(t *testing.T) {
	st := New(time.Second, 5*time.Second, discardLogger())
	st.Register(&fakeChecker{id: "p1"})
	st.Register(&fakeChecker{id: "p2"})
	if got := st.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
}
