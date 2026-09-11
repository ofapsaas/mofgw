// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package modelsdev

// Store: cache en disco del catálogo (D8 de 001). El mecanismo (flock
// LOCK_EX|LOCK_NB sobre lock file dedicado, digest sha256 en sidecar con
// commit sidecar-primero, TTL por mtime, fail-soft/fail-loud, escritura
// atómica temp+rename) vive en internal/modelscache; este archivo solo
// resuelve defaults de la fuente y expone la API de 019-001 vía alias de
// tipo (P2/P3, refactor mecánico 019-002).

import (
	"time"

	"github.com/ofapsaas/mofgw/internal/modelscache"
)

const (
	// DefaultCacheTTL es la frescura default del cache (D8 de 001); const
	// del motor, expuesta como alias (P2).
	DefaultCacheTTL = modelscache.DefaultCacheTTL
)

// Store es el cache en disco del catálogo de models.dev: alias del Store
// genérico del motor instanciado con *Catalog (D8 de 002 — P2: campos
// Path/TTL/Lock y métodos Get/Refresh idénticos a 001).
type Store = modelscache.Store[*Catalog]

// DefaultCachePath devuelve el path default del cache (D8 de 001):
// MOFGW_CACHE_DIR (directorio base, override) o os.UserCacheDir()/mofgw;
// si UserCacheDir falla (p.ej. HOME/XDG rotas) y no hay override →
// os.TempDir()/mofgw (#4 de 001: nunca CWD relativo). Firma string() de
// 001 conservada: wrapper sobre el DefaultCachePath(filename) del motor
// (D5 de 002).
func DefaultCachePath() string {
	return modelscache.DefaultCachePath("models-dev.json")
}

// NewStore construye el Store. path vacío → DefaultCachePath(); ttl <= 0 →
// DefaultCacheTTL (D8 de 001).
func NewStore(path string, ttl time.Duration, lock bool, opts ...Option) *Store {
	if path == "" {
		path = DefaultCachePath()
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return modelscache.NewStore[*Catalog](specModelsDev(), path, ttl, lock, opts...)
}
