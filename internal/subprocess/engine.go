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
// motor NO conoce strings backend-específicos: argv, traducción, parseo y
// detección de negativa de policy viven en la frontera Backend (I2).
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
// traducción de formato, parseo y detección de negativa de policy. El
// motor NO conoce strings backend-específicos.
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
	// TranslateStreamOut traduce líneas del stdout a eventos SSE. Recibe ctx
	// (D2): todo send a ch debe guardarse con select sobre ctx.Done() y
	// retornar si el ctx se cancela, para no bloquear ni colgar el lock.
	TranslateStreamOut(ctx context.Context, lines <-chan string, ch chan<- provider.StreamEvent, model string)
	// IsRefusal reporta si el stderr del CLI señala una negativa de
	// policy/safety (P11). Es responsabilidad del Backend reconocer la
	// señal concreta; el motor NO interpreta stderr backend-específico (I2).
	IsRefusal(stderr string) bool
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

	// sessionTTL/now (P5/P6): vida de una sesión en disco y clock inyectable
	// (precedente health.Store). now marca actividad y decide stale-ness en
	// el sweep. Defaults aplicados en NewProvider.
	sessionTTL time.Duration
	now        func() time.Time

	mu    sync.Mutex
	locks map[string]lockSlot // Session.ID -> slot (cap 1, D8) + última actividad (P6)
}

// lockSlot es una entrada del lock map: el canal de serialización por sesión
// (cap 1) y la última vez que fue usada (para la eviction idle, P6).
type lockSlot struct {
	slot     chan struct{}
	lastUsed time.Time
}

// DefaultSessionTTL es la vida por defecto de una sesión en disco antes de
// ser barrida por el TTL (P5, D1): 7 días (sesiones largas para prompt-cache;
// limpieza acotada).
const DefaultSessionTTL = 7 * 24 * time.Hour

// NewProvider construye el provider subprocess. Acepta opciones funcionales
// (ProviderOption) para inyectar sessionTTL/now en tests (P5/P6); los call
// sites existentes siguen funcionando sin opciones.
func NewProvider(id string, models []string, maxTokens int64, sessionDir string, backendFlags []string, backend Backend, logger *slog.Logger, opts ...ProviderOption) *Provider {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	p := &Provider{
		id:           id,
		models:       models,
		maxTokens:    maxTokens,
		sessionDir:   sessionDir,
		backendFlags: backendFlags,
		backend:      backend,
		logger:       logger,
		sessionTTL:   DefaultSessionTTL,
		now:          time.Now,
		locks:        make(map[string]lockSlot),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// ProviderOption configura un Provider en NewProvider (P5/P6, test hooks).
type ProviderOption func(*Provider)

// WithSessionTTL inyecta la vida de sesión (P5, default DefaultSessionTTL).
func WithSessionTTL(d time.Duration) ProviderOption {
	return func(p *Provider) { p.sessionTTL = d }
}

// WithNow inyecta el clock (P5/P6, default time.Now; precedente health.Store).
func WithNow(f func() time.Time) ProviderOption {
	return func(p *Provider) { p.now = f }
}

// SetSessionTTL sobreescribe el TTL de sesión en runtime (test hook P5).
func (p *Provider) SetSessionTTL(d time.Duration) { p.sessionTTL = d }

// SetNow sobreescribe el clock inyectable en runtime (test hook P5/P6).
func (p *Provider) SetNow(f func() time.Time) { p.now = f }

// LockCount devuelve el número de entradas del lock map en memoria (test
// hook P6; precedente health.Store.Len).
func (p *Provider) LockCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.locks)
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

// Models devuelve los modelos configurados (013-003 P6): accessor aditivo
// para el catálogo /v1/models. Copia defensiva para no exponer el slice
// interno (I1: Provider intacto, sin cambios de interfaz).
func (p *Provider) Models() []string { return append([]string(nil), p.models...) }

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
	cmd.Env = p.childEnv()
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
//
// El canal ch se drena por el consumidor. Para evitar un leak de
// goroutine y la inanición del lock por sesión (P8) si el consumidor
// abandona el canal (disconnect/timeout del proxy), todo send del motor a
// ch se guarda con select sobre ctx.Done(): ante cancelación se hace kill
// + reap del proceso y se retorna, liberando el lock. El scanner de stdout
// se guarda del mismo modo para no dejar una segunda goroutine colgada.
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
	cmd.Env = p.childEnv()

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
			// Guard contra un segundo leak: si el ctx se cancela, el
			// scanner deja de enviar y cierra lines.
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		defer release()

		// send emite a ch guardándose en ctx.Done(). Devuelve false si el
		// ctx se canceló (consumidor abandonó el canal): en ese caso hace
		// kill + reap del proceso y el goroutine retorna, liberando el
		// lock por sesión (P8) en vez de bloquear para siempre.
		send := func(ev provider.StreamEvent) bool {
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				_ = cmd.Wait() // reap: sin hijos huérfanos
				return false
			}
		}

		p.backend.TranslateStreamOut(ctx, lines, ch, model)

		// Si el ctx ya está cancelado al terminar la traducción, no
		// seguimos: kill + reap y salir (release corre vía defer).
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return
		}

		exitErr := cmd.Wait()
		if exitErr != nil {
			// mid-stream fallo: normalizado, nunca error crudo de ctx (P10).
			switch {
			case ctx.Err() != nil:
				send(provider.StreamEvent{Err: provider.NewErrUpstream(0, "timeout", "request cancelled or timed out")})
			case p.backend.IsRefusal(stderr.String()):
				send(provider.StreamEvent{Err: provider.NewErrUpstream(400, "invalid_request_error", "request refused by policy")})
			default:
				send(provider.StreamEvent{Err: provider.NewErrUpstream(500, "upstream_error", p.backend.Name()+" exited with error")})
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
		if !send(provider.StreamEvent{Data: string(payload)}) {
			return
		}
		if !send(provider.StreamEvent{Data: "[DONE]"}) {
			return
		}
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
	p.sweepStale()
	return &Session{ID: key, Dir: dir, ClientID: clientID}, nil
}

// acquireLock toma el lock de serialización por sesión (cap 1, D8) con
// espera ctx-cancelable (P9). Devuelve una función de release.
func (p *Provider) acquireLock(ctx context.Context, sessionID string) (func(), error) {
	p.mu.Lock()
	slot, ok := p.locks[sessionID]
	if !ok {
		slot = lockSlot{slot: make(chan struct{}, 1), lastUsed: p.now()}
		p.locks[sessionID] = slot
	}
	slot.lastUsed = p.now()
	p.locks[sessionID] = slot
	p.mu.Unlock()

	select {
	case slot.slot <- struct{}{}:
		return func() { <-slot.slot }, nil
	case <-ctx.Done():
		return nil, provider.NewErrUpstream(0, "timeout", "request cancelled or timed out")
	}
}

// sweepStale ejecuta el sweep de TTL (P5/P6): elimina dirs de cliente idle
// más allá del TTL y evicta las entradas del lock map idle y libres. RED:
// no-op — no implementado (T_ttl_stale_dir_removed / T_lock_map_bounded quedan
// RED por aserción).
func (p *Provider) sweepStale() {
	// RED: no implementado
}

// childEnv construye el env del proceso CLI hijo (P3/D3): allowlist fija
// PATH + HOME + variables STUB_*. RED: devuelve el env completo (os.Environ)
// — la allowlist NO está implementada (T_child_env_allowlist queda RED).
func (p *Provider) childEnv() []string {
	return os.Environ()
}

// normalizeExit convierte un fallo de ejecución del CLI a *ErrUpstream
// (P10/P11/P12): timeout de ctx ⇒ "timeout"; refusal de policy (señalada
// por el Backend vía IsRefusal, I2) ⇒ invalid_request_error/400
// no-retryable; cualquier otro exit no-cero ⇒ upstream_error/500 retryable.
// Todo mensaje es controlado y seguro (nunca el stderr crudo, que podría
// filtrar paths/argv).
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
	if p.backend.IsRefusal(stderr) {
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
