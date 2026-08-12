// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package subprocess implementa el motor genérico para providers que
// ejecutan el CLI de un backend de IA como subproceso (feature 013-001).
//
// El motor (GREEN) resuelve la sesión por cliente (auth.ClientIDFrom),
// serializa los requests por sesión (máximo un proceso CLI en vuelo por
// sesión), spawnea el CLI con el prompt único traducido por el Backend,
// fabrica el usage y normaliza TODO fallo a *provider.ErrUpstream. El
// motor NO conoce strings backend-específicos: argv, traducción y parseo
// viven en la frontera Backend (I2).
package subprocess

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ofapsaas/mofgw/internal/auth"
	"github.com/ofapsaas/mofgw/internal/provider"
)

// Session modela una sesión de cliente que vive en el proceso del CLI
// subprocess. ID se deriva determinísticamente del clientID (o
// clientID|model en modo client+model); Dir es la base de la sesión.
type Session struct {
	ID       string
	Dir      string
	ClientID string
}

// SessionKeyMode selecciona la derivación de la key de sesión.
type SessionKeyMode int

const (
	// SessionKeyClient deriva la key solo del clientID (default, D2).
	SessionKeyClient SessionKeyMode = iota
	// SessionKeyClientModel deriva la key de clientID|model (override).
	SessionKeyClientModel
)

// NewSessionKey deriva la key de sesión según el modo (P1/P2): hash
// sha256 hex truncado, determinístico y no vacío. En modo default (D2) la
// key se deriva SOLO del clientID (el modelo no participa); en modo
// client+model se deriva de clientID|model.
func NewSessionKey(clientID, model string, mode SessionKeyMode) string {
	seed := clientID
	if mode == SessionKeyClientModel {
		seed = clientID + "|" + model
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

// Backend es la frontera de traducción del motor (D1): posee argv,
// traducción de formato y parseo. El motor NO conoce strings
// backend-específicos.
type Backend interface {
	// Name identifica el backend (observabilidad).
	Name() string
	// Args arma el argv del CLI para un request (cwd = Session.Dir).
	// El modelo se pasa per-request; NO participa de la key de sesión.
	Args(s *Session, model string, flags []string) []string
	// TranslateReq traduce el body chat-completions a UN prompt único de
	// texto natural limpio (D3/D4).
	TranslateReq(body []byte) (string, error)
	// TranslateOut traduce stdout (modo no-stream) a ChatResponse OpenAI.
	TranslateOut(raw []byte, model string) (*provider.ChatResponse, error)
	// TranslateStreamOut traduce líneas del stdout (streaming) a eventos.
	TranslateStreamOut(lines <-chan string, ch chan<- provider.StreamEvent, model string)
}

// Provider implementa provider.Provider (I1) ejecutando un CLI como
// subproceso.
type Provider struct {
	id           string
	models       []string
	maxTokens    int64
	sessionDir   string
	backendFlags []string // opacos: pasan tal cual a Backend.Args (I2)
	backend      Backend
	logger       *slog.Logger

	mu    sync.Mutex
	locks map[string]chan struct{} // Session.ID -> slot de serialización (cap 1, D8)
}

// NewProvider construye el provider subprocess.
func NewProvider(id string, models []string, maxTokens int64, sessionDir string, backendFlags []string, backend Backend, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Provider{
		id:           id,
		models:       models,
		maxTokens:    maxTokens,
		sessionDir:   sessionDir,
		backendFlags: backendFlags,
		backend:      backend,
		logger:       logger,
		locks:        make(map[string]chan struct{}),
	}
}

// var _ garantiza en tiempo de compilación que Provider cumple I1.
var _ provider.Provider = (*Provider)(nil)

// ID devuelve el id del provider.
func (p *Provider) ID() string { return p.id }

// MaxTokens devuelve el límite de tokens.
func (p *Provider) MaxTokens() int { return int(p.maxTokens) }

// Serves reporta si el provider sirve el modelo pedido (P3).
func (p *Provider) Serves(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

// Complete ejecuta un chat no-stream (P2/P4/P6): resuelve la sesión,
// adquiere el lock por sesión, spawnea el CLI con el prompt único,
// captura stdout y normaliza el resultado/fallos.
func (p *Provider) Complete(ctx context.Context, body []byte) (*provider.CompleteResult, error) {
	clientID := auth.ClientIDFrom(ctx)
	sess, err := p.resolveSession(clientID)
	if err != nil {
		return nil, err // P13 fail-closed sin spawn
	}
	release, err := p.acquireLock(ctx, sess.ID)
	if err != nil {
		return nil, err // P9 espera cancelable
	}
	defer release()

	req, err := provider.ParseChatRequest(body)
	if err != nil {
		return nil, provider.NewErrUpstream(400, "invalid_request_error", "invalid request body")
	}
	model := req.Model

	prompt, err := p.backend.TranslateReq(body)
	if err != nil {
		return nil, provider.NewErrUpstream(400, "invalid_request_error", "translate request")
	}

	argv := p.backend.Args(sess, model, p.backendFlags)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = sess.Dir
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, p.normalizeExit(ctx, err, stderr.String())
	}

	raw := stdout.Bytes()
	resp, err := p.backend.TranslateOut(raw, model)
	if err != nil {
		return nil, provider.NewErrUpstream(0, "network", "invalid backend output")
	}
	p.fillResponse(resp, sess, model, len(body), len(raw))
	return &provider.CompleteResult{Response: resp, Raw: raw, ProviderID: p.id}, nil
}

// Stream ejecuta un chat streaming (P7/P8/P10): resuelve la sesión,
// adquiere el lock por sesión (lo mantiene durante TODO el stream),
// spawnea el CLI, traduce el stdout línea a línea y emite un chunk final
// con usage fabricado + [DONE]. Los errores pre-primer-byte vuelven como
// error de retorno (canal nil); los mid-stream viajan como StreamEvent.Err.
func (p *Provider) Stream(ctx context.Context, body []byte) (<-chan provider.StreamEvent, error) {
	clientID := auth.ClientIDFrom(ctx)
	sess, err := p.resolveSession(clientID)
	if err != nil {
		return nil, err // P13 fail-closed sin spawn
	}
	release, err := p.acquireLock(ctx, sess.ID)
	if err != nil {
		return nil, err // P9 espera cancelable
	}

	req, err := provider.ParseChatRequest(body)
	if err != nil {
		release()
		return nil, provider.NewErrUpstream(400, "invalid_request_error", "invalid request body")
	}
	model := req.Model

	prompt, err := p.backend.TranslateReq(body)
	if err != nil {
		release()
		return nil, provider.NewErrUpstream(400, "invalid_request_error", "translate request")
	}

	argv := p.backend.Args(sess, model, p.backendFlags)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = sess.Dir
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		release()
		return nil, provider.NewErrUpstream(0, "network", "pipe stdin")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		release()
		return nil, provider.NewErrUpstream(0, "network", "pipe stdout")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		release()
		return nil, provider.NewErrUpstream(0, "network", "spawn backend")
	}
	if _, err := stdin.Write([]byte(prompt)); err != nil {
		_ = cmd.Process.Kill()
		release()
		return nil, provider.NewErrUpstream(0, "network", "write stdin")
	}
	_ = stdin.Close()

	lines := make(chan string, 8)
	var outLen atomic.Int64
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			outLen.Add(int64(len(sc.Bytes())))
			lines <- sc.Text()
		}
	}()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		defer release()
		p.backend.TranslateStreamOut(lines, ch, model)

		exitErr := cmd.Wait()
		if exitErr != nil {
			// mid-stream fallo: normalizado, nunca error crudo de ctx (P10).
			if ctx.Err() != nil {
				ch <- provider.StreamEvent{Err: provider.NewErrUpstream(0, "timeout", "request cancelled or timed out")}
			} else if isRefusal(stderr.String()) {
				ch <- provider.StreamEvent{Err: provider.NewErrUpstream(400, "invalid_request_error", "request refused by policy")}
			} else {
				ch <- provider.StreamEvent{Err: provider.NewErrUpstream(500, "upstream_error", p.backend.Name()+" exited with error")}
			}
			return
		}

		// Chunk final con usage fabricado (P7) + terminador [DONE],
		// compatible con stream.CaptureUsage.
		usage := fabricateUsage(len(body), int(outLen.Load()))
		payload, _ := json.Marshal(map[string]any{
			"choices": []any{},
			"usage": map[string]any{
				"prompt_tokens":     usage.PromptTokens,
				"completion_tokens": usage.CompletionTokens,
				"total_tokens":      usage.TotalTokens,
			},
		})
		ch <- provider.StreamEvent{Data: string(payload)}
		ch <- provider.StreamEvent{Data: "[DONE]"}
	}()
	return ch, nil
}

// resolveSession resuelve la sesión de un request desde la identidad del
// cliente (P1/P13). clientID vacío ⇒ fail-closed sin spawn. Dir =
// sessionDir/clients/<clientID>/, creada con modo 0700.
func (p *Provider) resolveSession(clientID string) (*Session, error) {
	if clientID == "" {
		return nil, provider.NewErrUpstream(400, "invalid_request_error", "missing client identity")
	}
	key := NewSessionKey(clientID, "", SessionKeyClient)
	dir := filepath.Join(p.sessionDir, "clients", clientID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, provider.NewErrUpstream(0, "network", "create session dir")
	}
	return &Session{ID: key, Dir: dir, ClientID: clientID}, nil
}

// acquireLock toma el lock de serialización por sesión (cap 1, D8) con
// espera ctx-cancelable (P9). Devuelve una función de release.
func (p *Provider) acquireLock(ctx context.Context, sessionID string) (func(), error) {
	p.mu.Lock()
	slot, ok := p.locks[sessionID]
	if !ok {
		slot = make(chan struct{}, 1)
		p.locks[sessionID] = slot
	}
	p.mu.Unlock()

	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, provider.NewErrUpstream(0, "timeout", "request cancelled or timed out")
	}
}

// normalizeExit convierte un fallo de ejecución del CLI a *ErrUpstream
// (P10/P11/P12): timeout de ctx ⇒ "timeout"; refusal de policy ⇒
// invalid_request_error/400 no-retryable; cualquier otro exit no-cero ⇒
// upstream_error/500 retryable. Todo mensaje es controlado y seguro
// (nunca el stderr crudo, que podría filtrar paths/argv).
func (p *Provider) normalizeExit(ctx context.Context, runErr error, stderr string) error {
	if ctx.Err() != nil {
		return provider.NewErrUpstream(0, "timeout", "request cancelled or timed out")
	}
	// Un fallo de spawn/exec (el proceso nunca arrancó) no es un
	// *exec.ExitError ⇒ normaliza a network (P10a).
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return provider.NewErrUpstream(0, "network", "spawn backend")
	}
	if isRefusal(stderr) {
		return provider.NewErrUpstream(400, "invalid_request_error", "request refused by policy")
	}
	return provider.NewErrUpstream(500, "upstream_error", p.backend.Name()+" exited with error")
}

// fillResponse completa la respuesta parseada con los campos que el motor
// posee y fabrica el usage (P6).
func (p *Provider) fillResponse(resp *provider.ChatResponse, sess *Session, model string, promptBytes, completionBytes int) {
	resp.ID = "chatcmpl-" + sess.ID
	resp.Object = "chat.completion"
	resp.Created = time.Now().Unix()
	resp.Model = model
	resp.Usage = fabricateUsage(promptBytes, completionBytes)
}

// fabricateUsage estima un usage no degenerado (P6/P7): prompt ~
// promptBytes, completion ~ completionBytes, Total = Prompt + Completion.
func fabricateUsage(promptBytes, completionBytes int) provider.Usage {
	prompt := estimateTokens(promptBytes)
	completion := estimateTokens(completionBytes)
	return provider.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

// estimateTokens estima tokens desde un largo en bytes (~4 chars/token).
func estimateTokens(n int) int {
	if n <= 0 {
		return 0
	}
	return n / 4
}

// isRefusal detecta una señal genérica de negativa de policy/safety en el
// stderr del CLI (P11). El motor solo reconoce palabras clave genéricas;
// la señal concreta del backend la maneja el propio Backend.
func isRefusal(stderr string) bool {
	low := strings.ToLower(stderr)
	for _, kw := range []string{"refused", "policy", "safety"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}
