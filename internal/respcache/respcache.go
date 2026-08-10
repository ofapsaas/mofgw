// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package respcache implementa el cache de respuestas exact-match
// (010-002 P1): sirve SOLO requests byte-idénticos (canonicalizados),
// con LRU en memoria + TTL. Cero riesgo de respuesta stale a prompts
// "similares" — el match es determinístico.
//
// Contrato del cache (research-efficiency.md §4.2):
//   - Elegibilidad la decide el llamador (proxy): no-stream +
//     determinístico (temperature == 0 o seed presente).
//   - Key = sha256(canonical JSON del request) particionado por
//     client_id (cero fuga entre clientes aunque los prompts sean
//     idénticos).
//   - Entry = respuesta JSON completa (incl. usage) + createdAt para
//     calcular X-Mofgw-Cache-Age.
//   - Storage: LRU en memoria, max_entries (default 512), TTL (default
//     5 min) — respeta el principio de RAM ~12MB del gateway.
//
// Transparencia: el hit se expone vía headers X-Mofgw-Cache: HIT y
// X-Mofgw-Cache-Age; el cliente SIEMPRE puede ver que vino del cache
// (principio rector del gateway intacto).
package respcache

import (
	"container/list"
	"sync"
	"time"
)

// Entry es una respuesta cacheable completa, lista para reescribirse.
// Body es opaco para este paquete: el llamador decide (JSON de
// chat.completion). Usage no se replica acá — el body ya lo incluye.
type Entry struct {
	StatusCode  int
	ContentType string
	Body        []byte
	CreatedAt   time.Time
}

type item struct {
	key   string
	entry *Entry
}

// Cache es un LRU thread-safe con TTL. Front de la lista = más
// recientemente usado (MRU); Back = candidato a evicción (LRU).
type Cache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	lru        *list.List
	maxEntries int
	ttl        time.Duration
}

// New construye un Cache. maxEntries <= 0 → 512. ttl <= 0 → 5 min.
func New(maxEntries int, ttl time.Duration) *Cache {
	if maxEntries <= 0 {
		maxEntries = 512
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Cache{
		entries:    make(map[string]*list.Element),
		lru:        list.New(),
		maxEntries: maxEntries,
		ttl:        ttl,
	}
}

// Get devuelve la entrada si existe, no expiró y la mueve al frente
// (MRU). Un entry expirado se elimina (lazy eviction) y reporta miss.
func (c *Cache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	it := el.Value.(*item)
	if time.Since(it.entry.CreatedAt) > c.ttl {
		c.lru.Remove(el)
		delete(c.entries, key)
		return nil, false
	}
	c.lru.MoveToFront(el)
	return it.entry, true
}

// Set inserta o reemplaza una entrada, la marca MRU y evicta el LRU si
// el tamaño excede maxEntries.
func (c *Cache) Set(key string, e *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		el.Value.(*item).entry = e
		c.lru.MoveToFront(el)
		return
	}
	c.lru.PushFront(&item{key: key, entry: e})
	c.entries[key] = c.lru.Front()
	for c.lru.Len() > c.maxEntries {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		c.lru.Remove(oldest)
		delete(c.entries, oldest.Value.(*item).key)
	}
}

// Len devuelve la cantidad de entradas vigentes.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
