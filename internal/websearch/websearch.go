// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Package websearch implementa el cliente DuckDuckGo de producción del
// grounded search server-side (feature 011-005-web-search, D3). mofgw
// emula el grounded search emitiendo una búsqueda web por su cuenta: el
// cliente scrapea `html.duckduckgo.com/html/?q=<query>` (primario) y usa
// la Instant Answer API (`api.duckduckgo.com/?q=<query>&format=json`) como
// fallback si el scrape falla o devuelve vacío. Devuelve top-N
// {Title,URL,Snippet} o un error (que en el handler degrada a best-effort,
// P7). Sin librerías de terceros: solo net/http + stdlib.
//
// El contrato observable (Result / Client) es independiente del parser: el
// spec 011-005 exige top-N {title,url,snippet} (P3/P4) y eso es estable. La
// verificación empírica del soporte real de DDG queda como desviación no
// bloqueante (los tests usan mock, I6).
package websearch

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Result es un resultado de búsqueda web (D3/P3): el shape observable que
// 011-005 formatea en el system message grounded (P4). Cada item lleva
// Title, URL y Snippet (sin url_citation, I5/D4).
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Client es el contrato del buscador que 011-005 inyecta en el Server
// (D3): Search recibe la query (P2) y devuelve top-N {Title,URL,Snippet}
// (P3) o un error (degradable a best-effort, P7). El método toma un solo
// argumento (la query) y el timeout se gobierna por el http.Client del
// concreto, consistente con el mock del test (proxy_test).
type Client interface {
	Search(query string) ([]Result, error)
}

// DDG es el cliente de producción de DuckDuckGo (D3): scrape del HTML
// lite como primario, fallback a la Instant Answer API. Timeout y tope de
// resultados configurables.
type DDG struct {
	client *http.Client
	max    int
}

// New construye el cliente DDG. max <= 0 → default 3; timeout <= 0 →
// default 10s (best-effort, P7: un fallo de red degrada, no cuelga).
func New(max int, timeout time.Duration) *DDG {
	if max <= 0 {
		max = 3
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &DDG{client: &http.Client{Timeout: timeout}, max: max}
}

// Search ejecuta la query (P2): primero scrape del HTML lite; si falla o
// devuelve vacío, fallback a la Instant Answer API (P3). Ante ambos
// fallos → error (el handler degrada a best-effort, P7).
func (d *DDG) Search(query string) ([]Result, error) {
	if results, err := d.scrape(query); err == nil && len(results) > 0 {
		return results, nil
	}
	return d.instantAnswer(query)
}

// scrape parsea `html.duckduckgo.com/html/?q=<query>` (primario, P3).
func (d *DDG) scrape(query string) ([]Result, error) {
	u := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	resp, err := d.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ddg html: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	results := parseHTMLResults(string(body), d.max)
	if len(results) == 0 {
		return nil, fmt.Errorf("ddg html: sin resultados")
	}
	return results, nil
}

// instantAnswer consulta la Instant Answer API (fallback, P3).
func (d *DDG) instantAnswer(query string) ([]Result, error) {
	u := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json"
	resp, err := d.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ddg api: status %d", resp.StatusCode)
	}
	var payload struct {
		AbstractText  string `json:"AbstractText"`
		AbstractURL   string `json:"AbstractURL"`
		Heading       string `json:"Heading"`
		RelatedTopics []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	var results []Result
	if payload.AbstractText != "" {
		results = append(results, Result{Title: payload.Heading, URL: payload.AbstractURL, Snippet: payload.AbstractText})
	}
	for _, t := range payload.RelatedTopics {
		if t.FirstURL == "" {
			continue
		}
		results = append(results, Result{Title: t.Text, URL: t.FirstURL, Snippet: t.Text})
		if len(results) >= d.max {
			break
		}
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ddg api: sin resultados")
	}
	return results, nil
}

// ---- parsing del HTML lite de DDG (sin librerías de terceros, D3) ----
//
// El HTML lite agrupa cada resultado en bloques con un anchor
// `class="result__a"` (título + href) y un anchor `class="result__snippet"`
// (resumen). Se extraen por separado y se emparejan por índice (mismo orden
// en el HTML).

var (
	resultAnchorRe  = regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	resultSnippetRe = regexp.MustCompile(`(?s)<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)
	htmlTagRe       = regexp.MustCompile(`<[^>]+>`)
	redirectLinkRe  = regexp.MustCompile(`/l/\?uddg=`)
)

// parseHTMLResults extrae hasta max resultados {Title,URL,Snippet} del HTML
// lite. Devuelve solo los pares título+snippet bien formados; los
// desalineados se dropean conservando el orden de los restantes.
func parseHTMLResults(html string, max int) []Result {
	titles := resultAnchorRe.FindAllStringSubmatch(html, -1)
	snippets := resultSnippetRe.FindAllStringSubmatch(html, -1)
	results := make([]Result, 0, len(titles))
	for i, m := range titles {
		if max > 0 && len(results) >= max {
			break
		}
		if len(m) < 3 {
			continue
		}
		r := Result{
			Title: cleanText(m[2]),
			URL:   cleanURL(m[1]),
		}
		if i < len(snippets) && len(snippets[i]) > 1 {
			r.Snippet = cleanText(snippets[i][1])
		}
		if r.Title == "" {
			continue
		}
		results = append(results, r)
	}
	return results
}

// cleanText quita tags HTML y decodifica entidades del texto de un nodo.
func cleanText(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(htmlUnescape(s))
}

// cleanURL normaliza el href de un resultado: si es un redirect interno de
// DDG (`/l/?uddg=<url>`), extrae la URL destino; si no, descarta el `//`
// de protocolo relativo.
func cleanURL(href string) string {
	href = strings.TrimSpace(href)
	if redirectLinkRe.MatchString(href) {
		if u, err := url.Parse(href); err == nil {
			if v := u.Query().Get("uddg"); v != "" {
				return v
			}
		}
	}
	return strings.TrimPrefix(href, "//")
}

// htmlUnescape decodifica las entidades HTML comunes (sin x/net/html).
func htmlUnescape(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'",
		"&nbsp;", " ", "&rsquo;", "’", "&ldquo;", "“", "&rdquo;", "”",
	)
	return r.Replace(s)
}
