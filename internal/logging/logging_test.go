package logging

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	if RequestID(ctx) != "" {
		t.Fatal("sin id debería devolver ''")
	}
	ctx = WithRequestID(ctx, "abc123")
	if RequestID(ctx) != "abc123" {
		t.Fatalf("RequestID = %q, want abc123", RequestID(ctx))
	}
}

func TestWithRequestAddsID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	ctx := WithRequestID(context.Background(), "req-1")
	withReq := WithRequest(logger, ctx)
	withReq.Info("test")

	out := buf.String()
	if !strings.Contains(out, "req-1") {
		t.Fatalf("log sin request_id: %s", out)
	}
	if !strings.Contains(out, "\"msg\":\"test\"") {
		t.Fatalf("formato JSON no aplicado: %s", out)
	}
}

func TestWithRequestNoID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	WithRequest(logger, context.Background()).Info("sin id")
	if strings.Contains(buf.String(), "request_id") {
		t.Fatalf("request_id vacío no debería aparecer: %s", buf.String())
	}
}

// ---- 005-005-verbose: Build(level, logFile, stdoutWriter) ----
//
// Contrato de la API nueva (ver spec 005-005-verbose, tabla de destinos):
//
//	Build(slog.LevelInfo,  "",  w) → info a stdout (w)
//	Build(slog.LevelInfo,  "f", w) → info a stdout (w) + archivo f
//	Build(slog.LevelDebug, "",  w) → debug+ a stdout (w)
//	Build(slog.LevelDebug, "f", w) → debug SOLO archivo; info a stdout + archivo
//
// stdoutWriter es inyectable para tests (nil = os.Stdout). Devuelve el
// logger, un closer para el archivo (no-op si no hay archivo) y un error
// si el archivo no se puede abrir (fail-fast).

func TestDestinoInfoSinArchivo(t *testing.T) {
	var buf bytes.Buffer
	logger, closer, err := Build(slog.LevelInfo, "", &buf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer func() { _ = closer.Close() }()

	logger.Info("info-a-stdout")
	logger.Debug("debug-no-debe-salir")

	out := buf.String()
	if !strings.Contains(out, "info-a-stdout") {
		t.Fatalf("info sin archivo debería ir a stdout: %s", out)
	}
	if strings.Contains(out, "debug-no-debe-salir") {
		t.Fatalf("debug sin --verbose no debe emitirse: %s", out)
	}
}

func TestDestinoInfoConArchivo(t *testing.T) {
	var buf bytes.Buffer
	logFile := filepath.Join(t.TempDir(), "mofgw.log")
	logger, closer, err := Build(slog.LevelInfo, logFile, &buf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer func() { _ = closer.Close() }()

	logger.Info("info-a-ambos")

	if !strings.Contains(buf.String(), "info-a-ambos") {
		t.Fatalf("info con archivo debería ir a stdout: %s", buf.String())
	}
	// cerrar antes de leer para forzar flush del archivo
	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := readLogFile(t, logFile); !strings.Contains(got, "info-a-ambos") {
		t.Fatalf("info con archivo debería ir al archivo: %s", got)
	}
}

func TestDestinoVerboseSinArchivo(t *testing.T) {
	var buf bytes.Buffer
	logger, closer, err := Build(slog.LevelDebug, "", &buf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer func() { _ = closer.Close() }()

	logger.Debug("debug-a-stdout")

	if !strings.Contains(buf.String(), "debug-a-stdout") {
		t.Fatalf("debug con --verbose sin archivo debería ir a stdout: %s", buf.String())
	}
}

func TestDestinoVerboseConArchivo(t *testing.T) {
	var buf bytes.Buffer
	logFile := filepath.Join(t.TempDir(), "mofgw.log")
	logger, closer, err := Build(slog.LevelDebug, logFile, &buf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer func() { _ = closer.Close() }()

	logger.Debug("debug-solo-archivo")
	logger.Info("info-a-ambos")

	out := buf.String()
	if strings.Contains(out, "debug-solo-archivo") {
		t.Fatalf("debug con archivo NO debe ir a stdout: %s", out)
	}
	if !strings.Contains(out, "info-a-ambos") {
		t.Fatalf("info con archivo debería ir a stdout: %s", out)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got := readLogFile(t, logFile)
	if !strings.Contains(got, "debug-solo-archivo") {
		t.Fatalf("debug con archivo debería ir al archivo: %s", got)
	}
	if !strings.Contains(got, "info-a-ambos") {
		t.Fatalf("info con archivo debería ir al archivo: %s", got)
	}
}

func TestArchivoNoAbrible(t *testing.T) {
	var buf bytes.Buffer
	_, closer, err := Build(slog.LevelInfo, "/nonexistent-dir/x.log", &buf)
	if err == nil {
		if closer != nil {
			_ = closer.Close()
		}
		t.Fatal("Build con log-file no abrible debería devolver error (fail-fast)")
	}
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("leer archivo de log: %v", err)
	}
	return string(b)
}
