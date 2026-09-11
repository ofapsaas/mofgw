// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package modelsdev

// Store: cache en disco del catálogo (D8), con TTL por mtime, flock
// exclusivo no bloqueante sobre <Path>.lock (I10), digest sha256 en
// sidecar <Path>.sha256 con skip de writes byte-idénticos (D6/I4) y
// escritura atómica temp+rename (D10/I3, patrón internal/metrics/persist.go).

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
	// DefaultCacheTTL es la frescura default del cache (D8).
	DefaultCacheTTL = 5 * time.Minute

	// sidecarExt / lockExt: sidecars dedicados del cache (D6/D8/I10).
	sidecarExt = ".sha256"
	lockExt    = ".lock"
)

// Store es el cache en disco del catálogo de models.dev: Path es el archivo
// cache (byte-fiel al upstream), TTL define la frescura por mtime (D8) y
// Lock habilita el flock exclusivo no bloqueante sobre el lock file
// dedicado <Path>.lock (D8/I10 — nunca sobre el propio cache).
type Store struct {
	Path string
	TTL  time.Duration
	Lock bool

	opts Options
}

// NewStore construye el Store. path vacío → DefaultCachePath(); ttl <= 0 →
// DefaultCacheTTL (D8).
func NewStore(path string, ttl time.Duration, lock bool, opts ...Option) *Store {
	if path == "" {
		path = DefaultCachePath()
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Store{Path: path, TTL: ttl, Lock: lock, opts: resolveOptions(opts)}
}

// DefaultCachePath devuelve el path default del cache (D8):
// MOFGW_CACHE_DIR (directorio base, override) o os.UserCacheDir()/mofgw,
// siempre + models-dev.json.
func DefaultCachePath() string {
	base := os.Getenv("MOFGW_CACHE_DIR")
	if base == "" {
		if userCache, err := os.UserCacheDir(); err == nil {
			base = filepath.Join(userCache, "mofgw")
		}
	}
	return filepath.Join(base, "models-dev.json")
}

// Refresh actualiza el cache: lock (si Lock), knob de disable, chequeo de
// TTL por mtime, fetch, skip por digest y reescritura atómica (P5-P13).
//
// Orden LOCKEADO: el lock se adquiere ANTES del chequeo de TTL — la carrera
// que el lock existe para evitar es que dos procesos vean stale a la vez y
// fetcheen ambos.
//
// Fail-soft (D4): fetch fallido con cache en disco → (false, nil) + evento
// fetch_failed; el cache queda byte-intacto (I6). Fail-loud: sin cache Y
// sin fetch posible → error.
func (s *Store) Refresh(ctx context.Context, force bool) (bool, error) {
	if s.Lock {
		release, err := s.acquireLock()
		if err != nil {
			return false, err
		}
		defer release()
	}

	info, statErr := os.Stat(s.Path)

	if fetchDisabled() {
		if statErr == nil {
			return false, nil // D5: cache presente → se sirve, sin fetch
		}
		return false, fmt.Errorf("modelsdev: refresh: %w", ErrFetchDisabled)
	}
	if statErr == nil && !force && time.Since(info.ModTime()) < s.TTL {
		return false, nil // P5: cache fresco → no fetch
	}

	_, raw, fetchErr := fetchWith(ctx, s.opts)
	if fetchErr != nil {
		if statErr == nil {
			return false, nil // D4 fail-soft: stale sigue disponible
		}
		return false, fmt.Errorf("modelsdev: refresh: %w", fetchErr)
	}

	return s.persist(raw)
}

// Get sirve el catálogo desde el archivo de cache: jamás red, jamás fetch
// (I5/P1/P2). La frescura no bloquea la lectura (P2); el estado stale es
// decisión del llamador (evento cache_hit{stale}).
func (s *Store) Get() (*Catalog, error) {
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("modelsdev: get: %w", err)
	}
	cat, err := ParseCatalog(raw)
	if err != nil {
		return nil, fmt.Errorf("modelsdev: get: %w", err)
	}
	stale := false
	if info, err := os.Stat(s.Path); err == nil {
		stale = time.Since(info.ModTime()) >= s.TTL
	}
	s.opts.logger.Info("cache_hit", "stale", stale)
	return cat, nil
}

// persist aplica I4 (digest antes de escribir) y D6: body byte-idéntico al
// digest del sidecar → ni cache ni sidecar se reescriben (P8). Primer
// write exitoso sin sidecar previo → cache + sidecar, changed=true.
func (s *Store) persist(raw []byte) (bool, error) {
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])

	if existing, err := os.ReadFile(s.Path + sidecarExt); err == nil &&
		strings.TrimSpace(string(existing)) == digest {
		s.opts.logger.Info("skipped_identical", "sha256", digest)
		return false, nil
	}

	if err := writeFileAtomic(s.Path, raw); err != nil {
		return false, fmt.Errorf("modelsdev: persist: %w", err)
	}
	if err := writeFileAtomic(s.Path+sidecarExt, []byte(digest)); err != nil {
		return false, fmt.Errorf("modelsdev: persist: %w", err)
	}
	return true, nil
}

// acquireLock toma el flock exclusivo no bloqueante sobre <Path>.lock
// (D8/I10). Devuelve el release (el Close libera el flock). Si otro
// proceso lo sostiene → error rápido con ErrLockBusy envuelto + evento
// lock_busy{path}, sin espera indefinida (P9).
func (s *Store) acquireLock() (func(), error) {
	lockPath := s.Path + lockExt
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o750); err != nil {
		return nil, fmt.Errorf("modelsdev: lock: mkdir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("modelsdev: lock: open %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			s.opts.logger.Warn("lock_busy", "path", lockPath)
			return nil, fmt.Errorf("modelsdev: lock: %s: %w", lockPath, ErrLockBusy)
		}
		return nil, fmt.Errorf("modelsdev: lock: flock %s: %w", lockPath, err)
	}
	return func() { _ = f.Close() }, nil
}

// writeFileAtomic escribe data en path con temp+rename (D10/I3), replicando
// internal/metrics/persist.go SaveState: MkdirAll 0o750 → CreateTemp
// <base>.tmp-* → write → Sync → Close → Rename → defer Remove.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("modelsdev: write: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("modelsdev: write: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op si el rename ya ocurrió
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("modelsdev: write: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("modelsdev: write: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("modelsdev: write: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("modelsdev: write: rename: %w", err)
	}
	return nil
}
