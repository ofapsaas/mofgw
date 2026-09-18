// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

// metricssummary.go — endpoint read-only GET /v1/metrics/summary?date=
// (019-007 spec P1-P17): HTML self-contained con el consumo del día leído
// en streaming desde registry.jsonl + rotados logrotate (<base>.N).
//
// Auth Bearer implícita (la ruta NO está en publicPrefixes — I4).
// Read-only absoluto (D11): jamás escribe el registry, rotados ni nada.
// Sin dependencias nuevas (stdlib only, D9/I3).
package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// SetRegistryPath configura el path del registry.jsonl activo (D3). Los
// rotados se derivan mecánicamente: <dir>/<base>.N (convención logrotate).
// Pre-tráfico e inmutable después (patrón SetRegistry, I4). El método vive
// en proxy.go (junto a SetRegistry) — este comentario documenta el wiring.

// metricsSummaryRow: agregados por modelo (P9).
type metricsSummaryRow struct {
	Requests         int64
	Success          int64
	Errors           int64
	TokensPrompt     int64
	TokensCompletion int64
	TokensCache      int64
	TokensReasoning  int64
	CostUSD          float64
	CostUpPresente   int64
	CostUpNull       int64
}

// metricsSummaryResult: agregados del día (P9/P10/P11/P12).
type metricsSummaryResult struct {
	Date            string
	Rows            map[string]*metricsSummaryRow // keyed por model ("" = desconocido)
	ProvUpstream    int64
	ProvTable       int64
	ProvNone        int64
	Historicas      int64 // líneas sin los campos de 020-001 (cost_usd_src="")
	Corruptas       int64
	TotalTerminales int64
	FilesRead       []string
	MissingActive   bool // P5: archivo activo inexistente
}

// handleMetricsSummary sirve el reporte HTML del día (P1-P17).
func (s *Server) handleMetricsSummary(w http.ResponseWriter, r *http.Request) {
	// P3: date requerido, formato estricto YYYY-MM-DD (UTC).
	date := r.URL.Query().Get("date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		openAIError(w, http.StatusBadRequest, "date requerido en formato YYYY-MM-DD (UTC)", "invalid_request_error")
		return
	}
	// P4: knob vacío → 503 runtime (patrón clientconfig 016-001).
	if s.registryPath == "" {
		openAIError(w, http.StatusServiceUnavailable, "registry file not configured", "server_error")
		return
	}

	result := aggregateMetricsSummary(s.registryPath, date)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(renderMetricsSummary(result))
}

// aggregateMetricsSummary parsea en streaming activo + rotados (P13) con
// filtro estricto type=="terminal" (P6) y fecha UTC (P7).
func aggregateMetricsSummary(regPath, date string) *metricsSummaryResult {
	result := &metricsSummaryResult{Date: date, Rows: map[string]*metricsSummaryRow{}}

	files := []string{regPath}
	for n := 1; n <= 4; n++ { // P8: tope 5 archivos (activo + 4 rotados)
		p := fmt.Sprintf("%s.%d", regPath, n)
		if _, err := os.Stat(p); err != nil {
			break
		}
		files = append(files, p)
	}

	if _, err := os.Stat(regPath); os.IsNotExist(err) {
		result.MissingActive = true // P5: día sin datos es válido
	}

	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue // rotado ausente: stop natural del loop (P8)
		}
		result.FilesRead = append(result.FilesRead, f)
		f, err := os.Open(f)
		if err != nil {
			continue // P6/I6: fail-soft, sin abortar
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // P12: línea gigante descartada sin abortar
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var ev struct {
				Type    string   `json:"type"`
				Ts      string   `json:"ts"`
				Outcome string   `json:"outcome"`
				Model   string   `json:"model"`
				CostUSD *float64 `json:"cost_usd"`
				CostSrc string   `json:"cost_usd_src"`
				CostUp  *float64 `json:"cost_usd_up"`
				Tokens  *struct {
					Prompt     int64 `json:"prompt"`
					Completion int64 `json:"completion"`
					Cache      int64 `json:"cache"`
					Reasoning  int64 `json:"reasoning"`
				} `json:"tokens"`
			}
			if err := json.Unmarshal(line, &ev); err != nil {
				result.Corruptas++ // P12: skip + contador, sin abortar
				continue
			}
			if ev.Type != "terminal" { // P6: solo terminales agregan
				continue
			}
			if len(ev.Ts) < 10 || ev.Ts[:10] != date { // P7: prefijo ts[:10] == date (UTC)
				continue
			}
			result.TotalTerminales++
			if ev.Model == "" {
				// P11: fila desconocido (histórico pre-020-001 o modelo ausente)
			}
			row := result.Rows[ev.Model]
			if row == nil {
				row = &metricsSummaryRow{}
				result.Rows[ev.Model] = row
			}
			row.Requests++
			if ev.Outcome == "success" {
				row.Success++
			} else {
				row.Errors++
			}
			if ev.Tokens != nil {
				row.TokensPrompt += ev.Tokens.Prompt
				row.TokensCompletion += ev.Tokens.Completion
				row.TokensCache += ev.Tokens.Cache
				row.TokensReasoning += ev.Tokens.Reasoning
			}
			if ev.CostUSD != nil {
				row.CostUSD += *ev.CostUSD
			}
			if ev.CostUp != nil {
				row.CostUpPresente++
			} else {
				row.CostUpNull++
			}
			// P10: cobertura de proveniencia ("" → none, D8)
			switch ev.CostSrc {
			case "upstream":
				result.ProvUpstream++
			case "table":
				result.ProvTable++
			default:
				result.ProvNone++
				if ev.CostSrc == "" {
					result.Historicas++
				}
			}
		}
		_ = f.Close()
	}
	return result
}

// renderMetricsSummary produce el HTML self-contained (P14/D9).
// Determinístico: filas en orden alfabético de modelo (D9 de 003).
func renderMetricsSummary(r *metricsSummaryResult) []byte {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"es\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<title>metrics summary " + r.Date + " (UTC) — mofgw</title>\n")
	b.WriteString("<style>\n")
	b.WriteString("body{font-family:monospace;margin:2em;background:#1a1a2e;color:#e0e0e0}\n")
	b.WriteString("h1{color:#0f3460}h2{color:#16213e}\n")
	b.WriteString("table{border-collapse:collapse;margin:1em 0}\n")
	b.WriteString("th,td{border:1px solid #16213e;padding:0.4em 0.8em;text-align:right}\n")
	b.WriteString("th{background:#16213e;color:#e0e0e0}\n")
	b.WriteString("td:first-child,th:first-child{text-align:left}\n")
	b.WriteString(".warn{color:#e94560}.ok{color:#0f9b0f}\n")
	b.WriteString(".note{color:#a0a0b0;font-size:0.85em}\n")
	b.WriteString("</style>\n</head>\n<body>\n")
	fmt.Fprintf(&b, "<h1>metrics summary — %s (UTC)</h1>\n", r.Date)

	if r.MissingActive {
		b.WriteString("<p class=\"warn\">archivo no encontrado (sin datos para este host)</p>\n")
	}

	// Filas sortadas por modelo (determinismo, D9 de 003).
	models := make([]string, 0, len(r.Rows))
	for m := range r.Rows {
		models = append(models, m)
	}
	sort.Strings(models)

	b.WriteString("<h2>Por modelo</h2>\n<table>\n<tr>")
	b.WriteString("<th>model</th><th>requests</th><th>success</th><th>error</th>")
	b.WriteString("<th>tokens.prompt</th><th>tokens.completion</th><th>tokens.cache</th><th>tokens.reasoning</th>")
	b.WriteString("<th>cost_usd</th><th>cost_usd_up presente</th><th>cost_usd_up null</th>")
	b.WriteString("</tr>\n")

	total := &metricsSummaryRow{}
	for _, m := range models {
		row := r.Rows[m]
		display := m
		if display == "" {
			display = "desconocido"
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td><td>%d</td><td>%.6f</td><td>%d</td><td>%d</td></tr>\n",
			display, row.Requests, row.Success, row.Errors,
			row.TokensPrompt, row.TokensCompletion, row.TokensCache, row.TokensReasoning,
			row.CostUSD, row.CostUpPresente, row.CostUpNull)
		total.Requests += row.Requests
		total.Success += row.Success
		total.Errors += row.Errors
		total.TokensPrompt += row.TokensPrompt
		total.TokensCompletion += row.TokensCompletion
		total.TokensCache += row.TokensCache
		total.TokensReasoning += row.TokensReasoning
		total.CostUSD += row.CostUSD
		total.CostUpPresente += row.CostUpPresente
		total.CostUpNull += row.CostUpNull
	}
	fmt.Fprintf(&b, "<tr><th>total</th><th>%d</th><th>%d</th><th>%d</th><th>%d</th><th>%d</th><th>%d</th><th>%d</th><th>%.6f</th><th>%d</th><th>%d</th></tr>\n",
		total.Requests, total.Success, total.Errors,
		total.TokensPrompt, total.TokensCompletion, total.TokensCache, total.TokensReasoning,
		total.CostUSD, total.CostUpPresente, total.CostUpNull)
	b.WriteString("</table>\n")

	// Cobertura de proveniencia (P10).
	coverage := 0.0
	if r.TotalTerminales > 0 {
		coverage = float64(r.ProvUpstream+r.ProvTable) / float64(r.TotalTerminales) * 100
	}
	b.WriteString("<h2>Cobertura de proveniencia</h2>\n<table>\n")
	fmt.Fprintf(&b, "<tr><td>upstream</td><td>%d</td></tr>\n", r.ProvUpstream)
	fmt.Fprintf(&b, "<tr><td>table</td><td>%d</td></tr>\n", r.ProvTable)
	fmt.Fprintf(&b, "<tr><td>none</td><td>%d</td></tr>\n", r.ProvNone)
	fmt.Fprintf(&b, "<tr><td>históricas (sin src)</td><td>%d</td></tr>\n", r.Historicas)
	fmt.Fprintf(&b, "<tr><th>cobertura (upstream+table)/total</th><td>%.1f%%</td></tr>\n", coverage)
	b.WriteString("</table>\n")

	// Contadores P12/P11.
	fmt.Fprintf(&b, "<p class=\"note\">líneas corruptas: %d | terminales del día: %d | archivos leídos: %d</p>\n",
		r.Corruptas, r.TotalTerminales, len(r.FilesRead))
	b.WriteString("<p class=\"note\">la fecha es UTC (el ts del registro es UTC)</p>\n")
	b.WriteString("</body>\n</html>\n")
	return []byte(b.String())
}
