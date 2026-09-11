// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package modelscache

// Store genérico (generalización de modelsdev/store.go de 001): cache en
// disco con TTL por mtime, flock exclusivo no bloqueante sobre
// <Path>.lock (I10), digest sha256 en sidecar <Path>.sha256 con skip de
// writes byte-idénticos (I4/D6) y escritura atómica temp+rename (I3,
// patrón internal/metrics/persist.go).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultCacheTTL es la frescura default del cache (D8 de 001 / D7 de 002).
	DefaultCacheTTL = 5 * time.Minute

	// sidecarExt / lockExt: sidecars dedicados del cache (D6/D8/I10 de 001).
	sidecarExt = ".sha256"
	lockExt    = ".lock"
)

// DefaultCachePath devuelve el path default del cache de una fuente
// (generalización de D8 de 001, D5 de 002): MOFGW_CACHE_DIR (directorio
// base, override) o os.UserCacheDir()/mofgw; si UserCacheDir falla (p.ej.
// HOME/XDG rotas) y no hay override → os.TempDir()/mofgw (#4 de 001:
// nunca CWD relativo, que dejaría el path dependiente del directorio de
// ejecución). filename lo aporta cada fuente.
func DefaultCachePath(filename string) string {
	base := os.Getenv("MOFGW_CACHE_DIR")
	if base == "" {
		if userCache, err := os.UserCacheDir(); err == nil {
			base = filepath.Join(userCache, "mofgw")
		} else {
			base = filepath.Join(os.TempDir(), "mofgw")
		}
	}
	return filepath.Join(base, filename)
}

// Store es el cache en disco de una fuente, genérico en el tipo parseado
// T: Path es el archivo cache (byte-fiel al upstream), TTL define la
// frescura por mtime (D8 de 001) y Lock habilita el flock exclusivo no
// bloqueante sobre el lock file dedicado <Path>.lock (D8/I10 — nunca
// sobre el propio cache).
type Store[T any] struct {
	Path string
	TTL  time.Duration
	Lock bool

	spec Spec
	opts Options
}

// NewStore construye el Store genérico de una fuente. path vacío lo
// resuelve la fuente (Default*CachePath — D5 de 002); ttl <= 0 →
// DefaultCacheTTL (D8 de 001).
func NewStore[T any](spec Spec, path string, ttl time.Duration, lock bool, opts ...Option) *Store[T] {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Store[T]{Path: path, TTL: ttl, Lock: lock, spec: spec, opts: resolveOptions(spec, opts)}
}

// Refresh actualiza el cache: lock (si Lock), knob de disable, chequeo de
// TTL por mtime, fetch, skip por digest y reescritura atómica (P5-P13 de
// 001).
//
// Orden LOCKEADO: el lock se adquiere ANTES del chequeo de TTL — la
// carrera que el lock existe para evitar es que dos procesos vean stale a
// la vez y fetcheen ambos.
//
// Fail-soft (D4 de 001): fetch fallido con cache en disco → (false, nil) +
// evento fetch_failed; el cache queda byte-intacto (I6). Fail-loud: sin
// cache Y sin fetch posible → error.
func (s *Store[T]) Refresh(ctx context.Context, force bool) (bool, error) {
	if s.Lock {
		release, err := s.acquireLock()
		if err != nil {
			return false, err
		}
		defer release()
	}

	info, statErr := os.Stat(s.Path)

	if fetchDisabled(s.spec) {
		if statErr == nil {
			return false, nil // D5 de 001: cache presente → se sirve, sin fetch
		}
		return false, fmt.Errorf("modelscache: refresh: %w", ErrFetchDisabled)
	}
	if statErr == nil && !force && time.Since(info.ModTime()) < s.TTL {
		return false, nil // P5 de 001: cache fresco → no fetch
	}

	_, raw, fetchErr := fetchAnyWith(ctx, s.spec, s.opts)
	if fetchErr != nil {
		if statErr == nil {
			return false, nil // D4 de 001 fail-soft: stale sigue disponible
		}
		return false, fmt.Errorf("modelscache: refresh: %w", fetchErr)
	}

	return s.persist(raw)
}

// Get sirve el valor parseado desde el archivo de cache: jamás red, jamás
// fetch (I5/P1/P2 de 001). La frescura no bloquea la lectura (P2); el
// estado stale es decisión del llamador (evento cache_hit{stale}).
func (s *Store[T]) Get() (T, error) {
	var zero T
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return zero, fmt.Errorf("modelscache: get: %w", err)
	}
	parsed, err := s.spec.Parse(raw)
	if err != nil {
		return zero, fmt.Errorf("modelscache: get: %w", err)
	}
	val, ok := parsed.(T)
	if !ok {
		return zero, fmt.Errorf("modelscache: get: el parse de la fuente devolvió %T, want %T", parsed, zero)
	}
	stale := false
	if info, err := os.Stat(s.Path); err == nil {
		stale = time.Since(info.ModTime()) >= s.TTL
	}
	s.opts.logger.Info("cache_hit", sourceAttrs(s.spec, "stale", stale)...)
	return val, nil
}

// persist aplica I4 (digest antes de escribir) y D6 de 001: body
// byte-idéntico al digest del sidecar → ni cache ni sidecar se reescriben
// (P8). Primer write exitoso sin sidecar previo → cache + sidecar,
// changed=true.
//
// Orden de commit (decisión HITL B2 2026-09-11, review 019-001): sidecar
// PRIMERO, cache ÚLTIMO — el cache es el punto de commit. Fallo del
// sidecar → cache byte-intacto + (false, err) (I6 consistente). Fallo del
// cache tras sidecar OK → sidecar adelantado; el próximo refresh detecta
// digest ≠ sidecar y reescribe ambos (auto-cura); en el interim Get()
// sirve el cache viejo (aceptable).
func (s *Store[T]) persist(raw []byte) (bool, error) {
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])

	if existing, err := os.ReadFile(s.Path + sidecarExt); err == nil &&
		strings.TrimSpace(string(existing)) == digest {
		s.opts.logger.Info("skipped_identical", sourceAttrs(s.spec, "sha256", digest)...)
		return false, nil
	}

	if err := writeFileAtomic(s.Path+sidecarExt, []byte(digest)); err != nil {
		return false, fmt.Errorf("modelscache: persist: %w", err)
	}
	if err := writeFileAtomic(s.Path, raw); err != nil {
		return false, fmt.Errorf("modelscache: persist: %w", err)
	}
	return true, nil
}

// acquireLock toma el flock exclusivo no bloqueante sobre <Path>.lock
// (D8/I10 de 001). Devuelve el release (el Close libera el flock). Si
// otro proceso lo sostiene → error rápido con ErrLockBusy envuelto +
// evento lock_busy{path}, sin espera indefinida (P9 de 001).
func (s *Store[T]) acquireLock() (func(), error) {
	lockPath := s.Path + lockExt
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o750); err != nil {
		return nil, fmt.Errorf("modelscache: lock: mkdir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("modelscache: lock: open %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			s.opts.logger.Warn("lock_busy", sourceAttrs(s.spec, "path", lockPath)...)
			return nil, fmt.Errorf("modelscache: lock: %s: %w", lockPath, ErrLockBusy)
		}
		return nil, fmt.Errorf("modelscache: lock: flock %s: %w", lockPath, err)
	}
	return func() { _ = f.Close() }, nil
}

// writeFileAtomic escribe data en path con temp+rename (D10/I3 de 001),
// replicando internal/metrics/persist.go SaveState: MkdirAll 0o750 →
// CreateTemp <base>.tmp-* → write → Sync → Close → Rename → defer Remove.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("modelscache: write: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("modelscache: write: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op si el rename ya ocurrió
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("modelscache: write: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("modelscache: write: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("modelscache: write: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("modelscache: write: rename: %w", err)
	}
	return nil
}
