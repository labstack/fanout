package ai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

// suggestBlocksFromQueryText parses a query tool's JSON result envelope
// and suggests visualization blocks based on the result shape.
// Returns nil if no blocks are appropriate.
func suggestBlocksFromQueryText(text string) []Block {
	// Parse the query result envelope: {"type":"query","data":{"results":[...],...},...}
	var envelope struct {
		Type string `json:"type"`
		Data struct {
			Results []map[string]any `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return nil
	}
	return suggestBlocksFromRows(envelope.Data.Results)
}

// suggestBlocksFromRows analyzes query result rows and builds appropriate
// visualization blocks. Currently detects heatmap patterns (time x bucket x count).
func suggestBlocksFromRows(results []map[string]any) []Block {
	if len(results) == 0 {
		return nil
	}

	cols := columnNames(results)

	// Heatmap: time-like + bucket/category + count columns
	if tc, bc, vc := detectHeatmapCols(cols, results); tc != "" && bc != "" && vc != "" {
		if b := buildHeatmapBlock(results, tc, bc, vc); b != nil {
			return []Block{*b}
		}
	}

	return nil
}

// columnNames returns sorted column names from the first result row.
func columnNames(results []map[string]any) []string {
	if len(results) == 0 {
		return nil
	}
	cols := make([]string, 0, len(results[0]))
	for k := range results[0] {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

// detectHeatmapCols looks for time, bucket, and count columns in query results.
// Returns empty strings if the pattern doesn't match.
func detectHeatmapCols(cols []string, results []map[string]any) (timeCol, bucketCol, valueCol string) {
	for _, c := range cols {
		cl := strings.ToLower(c)
		if timeCol == "" && isTimeLikeCol(cl) && isTimeLikeValues(results, c) {
			timeCol = c
			continue
		}
		if bucketCol == "" && isBucketLikeCol(cl) && isStringCol(results, c) && !isTimeLikeValues(results, c) {
			bucketCol = c
			continue
		}
		if valueCol == "" && isCountLikeCol(cl) && isNumericCol(results, c) {
			valueCol = c
		}
	}
	return
}

func isTimeLikeCol(cl string) bool {
	return cl == "bucket" || cl == "time" || cl == "time_bucket" ||
		cl == "ts" || cl == "timestamp" ||
		strings.Contains(cl, "time") || strings.HasSuffix(cl, "_bucket")
}

func isBucketLikeCol(cl string) bool {
	return cl == "latency_bucket" || cl == "bucket_label" ||
		cl == "duration_bucket" || cl == "range" ||
		strings.Contains(cl, "bucket") || strings.Contains(cl, "range") ||
		strings.Contains(cl, "band")
}

func isCountLikeCol(cl string) bool {
	return cl == "count" || cl == "cnt" || cl == "value" || cl == "total" || cl == "n"
}

func isTimeLikeValues(results []map[string]any, col string) bool {
	if len(results) == 0 {
		return false
	}
	s, ok := results[0][col].(string)
	if !ok {
		return false
	}
	// ISO timestamps: "2026-03-27T10:00:00" or "2026-03-27 10:00:00"
	return len(s) >= 10 && (strings.Contains(s, "T") || (s[4] == '-' && s[7] == '-'))
}

func isStringCol(results []map[string]any, col string) bool {
	if len(results) == 0 {
		return false
	}
	_, ok := results[0][col].(string)
	return ok
}

func isNumericCol(results []map[string]any, col string) bool {
	if len(results) == 0 {
		return false
	}
	switch results[0][col].(type) {
	case float64, int64, int, json.Number:
		return true
	}
	return false
}

// buildHeatmapBlock pivots flat rows (time, bucket, value) into a 2D heatmap block.
func buildHeatmapBlock(results []map[string]any, timeCol, bucketCol, valueCol string) *Block {
	// Collect unique times and buckets preserving insertion order
	timeSet := map[string]int{}
	bucketSet := map[string]int{}
	var times []string
	var bucketLabels []string

	for _, row := range results {
		t := fmt.Sprint(row[timeCol])
		b := fmt.Sprint(row[bucketCol])
		if _, ok := timeSet[t]; !ok {
			timeSet[t] = len(times)
			times = append(times, t)
		}
		if _, ok := bucketSet[b]; !ok {
			bucketSet[b] = len(bucketLabels)
			bucketLabels = append(bucketLabels, b)
		}
	}

	if len(times) < 2 || len(bucketLabels) < 2 {
		return nil // Not enough data for a meaningful heatmap
	}

	// Build 2D values array: values[timeIdx][bucketIdx]
	values := make([][]float64, len(times))
	for i := range values {
		values[i] = make([]float64, len(bucketLabels))
	}

	for _, row := range results {
		t := fmt.Sprint(row[timeCol])
		b := fmt.Sprint(row[bucketCol])
		ti := timeSet[t]
		bi := bucketSet[b]
		values[ti][bi] = toFloat64(row[valueCol])
	}

	// Convert bucket labels to numeric values for the schema (buckets is []float64).
	// Try to parse boundaries from labels like "0-10ms", "10-50ms", ">1s".
	// Fall back to sequential indices if parsing fails.
	bucketNums := make([]float64, len(bucketLabels))
	parsed := 0
	for i, label := range bucketLabels {
		if v, ok := parseBucketBoundary(label); ok {
			bucketNums[i] = v
			parsed++
		}
	}
	if parsed < len(bucketLabels)/2 {
		// Parsing didn't work for most labels — use indices
		for i := range bucketLabels {
			bucketNums[i] = float64(i)
		}
	}

	// Shorten time labels for display
	shortTimes := make([]string, len(times))
	for i, t := range times {
		shortTimes[i] = shortenTimestamp(t)
	}

	data := HeatmapBlockData{
		Title:   "Latency Distribution",
		Buckets: bucketNums,
		Times:   shortTimes,
		Values:  values,
	}

	b := NewBlock(BlockHeatmap, data)
	slog.Info("suggested heatmap block from query results",
		"times", len(times), "buckets", len(bucketLabels))
	return &b
}

// parseBucketBoundary extracts a numeric boundary from a bucket label string.
// Examples: "0-10ms" → 10, "10-50ms" → 50, "500ms-1s" → 1000, ">1s" → 5000.
// Returns (value, true) on success.
func parseBucketBoundary(label string) (float64, bool) {
	label = strings.TrimSpace(label)

	// Handle ">Xs" or ">=Xs" patterns
	if strings.HasPrefix(label, ">") {
		s := strings.TrimLeft(label, ">= ")
		if v, unit, ok := parseValueWithUnit(s); ok {
			return applyUnit(v, unit) * 5, true // Scale up overflow bucket
		}
		return 0, false
	}

	// Handle "A-Bms" or "A-Bs" range patterns
	// Strip the unit suffix first
	unit := ""
	stripped := label
	if strings.HasSuffix(stripped, "ms") {
		unit = "ms"
		stripped = strings.TrimSuffix(stripped, "ms")
	} else if strings.HasSuffix(stripped, "s") {
		unit = "s"
		stripped = strings.TrimSuffix(stripped, "s")
	}

	parts := strings.SplitN(stripped, "-", 2)
	if len(parts) == 2 {
		if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
			return applyUnit(v, unit), true
		}
	}

	// Try parsing as a plain number
	if v, err := strconv.ParseFloat(label, 64); err == nil {
		return v, true
	}

	return 0, false
}

// parseValueWithUnit splits "10ms" or "1s" into (value, unit).
func parseValueWithUnit(s string) (float64, string, bool) {
	if strings.HasSuffix(s, "ms") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "ms"), 64)
		return v, "ms", err == nil
	}
	if strings.HasSuffix(s, "s") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
		return v, "s", err == nil
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, "", err == nil
}

func applyUnit(v float64, unit string) float64 {
	switch unit {
	case "s":
		return v * 1000 // Convert to ms
	default:
		return v
	}
}

// shortenTimestamp shortens ISO timestamps to "HH:MM" for display.
func shortenTimestamp(t string) string {
	// "2026-03-27T10:00:00" → "10:00"
	// "2026-03-27 10:00:00" → "10:00"
	if len(t) >= 16 {
		if t[10] == 'T' || t[10] == ' ' {
			return t[11:16]
		}
	}
	return t
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}
