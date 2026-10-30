package fanql

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/telemetry"
)

type expect struct {
	Class string
	Field *fieldName
	Func  string
	Arg   int
	Scope string
}

func completionPrefix(text string, cursor int, source string) (token lexToken, p *parser) {
	runes := []rune(text)
	cursor = max(0, min(cursor, len(runes)))
	token = lexToken{Kind: eof, Span: span{cursor, cursor}}
	for _, candidate := range lex(text) {
		if candidate.Kind == eof {
			break
		}
		if candidate.Span.Start <= cursor && cursor <= candidate.Span.End {
			switch candidate.Kind {
			case tokenWord, tokenIdent, tokenPath, tokenString, tokenRaw, tokenBracketKey, tokenNumber:
				token = candidate
			}
		}
	}
	start := token.Span.Start
	prefix := string(runes[:start])
	p = &parser{vocab: currentVocabulary(), runes: []rune(prefix), tokens: lex(prefix), bounded: true, heights: make(map[any]int), completion: true, completionUsed: make(map[string]bool)}
	defer func() {
		if v := recover(); v != nil {
			switch v.(type) {
			case completionEnd, parseBound:
			default:
				panic(v)
			}
		}
	}()
	if len(p.runes) > maxTextRunes || len(prefix) > maxTextBytes || len(p.tokens) > maxTokens+1 {
		return token, p
	}
	p.query(source)
	return token, p
}

type schemaSource interface {
	Schema(context.Context, panel.SchemaRequest) (*panel.Schema, error)
}

type CompleteRequest struct {
	Text      string
	Cursor    int
	Source    string
	Viz       string
	Window    string
	Namespace string
	Variables []panel.Variable
}

type Item struct {
	Label  string `json:"label"`
	Insert string `json:"insert"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type SchemaStatus struct {
	Window  string `json:"window"`
	Sampled bool   `json:"sampled"`
	Warm    bool   `json:"warm"`
	Recent  bool   `json:"recent"`
	Error   bool   `json:"error,omitempty"`
}

type CompleteResponse struct {
	Replace panel.TextSpan `json:"replace"`
	Items   []Item         `json:"items"`
	Schema  SchemaStatus   `json:"schema"`
}

const maxCompletionItems = 50

type completionKey struct{ window, namespace string }
type completionSchemaEntry struct {
	schema  *panel.Schema
	expires time.Time
	failed  bool
}

// Schema work is admitted before spawning across all Completers. No request
// waits or queues work when the slots are full; a later completion can retry.
var completionSchemaSlots = make(chan struct{}, 4)

type Completer struct {
	source   schemaSource
	mu       sync.Mutex
	inflight map[completionKey]chan struct{}
	cache    map[completionKey]completionSchemaEntry
	now      func() time.Time
}

func NewCompleter(source schemaSource) *Completer {
	return &Completer{source: source, inflight: make(map[completionKey]chan struct{}), cache: make(map[completionKey]completionSchemaEntry), now: time.Now}
}

func completionWindow(window string) (string, bool) {
	if window == "" {
		return "24h", true
	}
	d, err := time.ParseDuration(window)
	if strings.HasSuffix(window, "d") {
		var days float64
		days, err = strconv.ParseFloat(strings.TrimSuffix(window, "d"), 64)
		d = time.Duration(days * float64(24*time.Hour))
	}
	if err != nil || d <= 0 {
		return "24h", true
	}
	if d > 24*time.Hour {
		return "24h", true
	}
	for _, name := range []string{"5m", "15m", "1h", "3h", "6h", "12h", "24h"} {
		n, _ := time.ParseDuration(name)
		if d <= n {
			return name, false
		}
	}
	return "24h", false
}

func (c *Completer) schema(ctx context.Context, key completionKey) (*panel.Schema, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if entry, ok := c.cache[key]; ok && now.Before(entry.expires) {
		return entry.schema, entry.schema != nil, entry.failed
	}
	if c.source == nil {
		return nil, false, false
	}
	if _, ok := c.inflight[key]; !ok {
		select {
		case completionSchemaSlots <- struct{}{}:
		default:
			return nil, false, false
		}
		done := make(chan struct{})
		c.inflight[key] = done
		go c.fetch(context.WithoutCancel(ctx), key, done)
	}
	return nil, false, false
}

func (c *Completer) fetch(parent context.Context, key completionKey, done chan struct{}) {
	defer func() { <-completionSchemaSlots }()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	schema, err := c.source.Schema(ctx, panel.SchemaRequest{Window: key.window, Namespace: key.namespace})
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	ttl := time.Minute
	failed := err != nil || schema == nil
	if failed {
		ttl = 10 * time.Second
		schema = nil
	}
	for k, entry := range c.cache {
		if !now.Before(entry.expires) {
			delete(c.cache, k)
		}
	}
	if len(c.cache) >= 64 {
		clear(c.cache)
	}
	c.cache[key] = completionSchemaEntry{schema: schema, expires: now.Add(ttl), failed: failed}
	delete(c.inflight, key)
	close(done)
}

type completionCandidate struct {
	item                Item
	class, match, boost int
	count               int64
	used                bool
}

type completionCandidates struct {
	req            CompleteRequest
	token          lexToken
	p              *parser
	signal         panel.SignalSchema
	source, prefix string
	items          []completionCandidate
	class          int
}

func (c *Completer) Complete(ctx context.Context, req CompleteRequest) CompleteResponse {
	window, recent := completionWindow(req.Window)
	schema, warm, failed := c.schema(ctx, completionKey{window, req.Namespace})
	token, p := completionPrefix(req.Text, req.Cursor, req.Source)
	r := CompleteResponse{Replace: panel.TextSpan{Start: token.Span.Start, End: token.Span.End}, Items: []Item{}, Schema: SchemaStatus{Window: window, Sampled: true, Warm: warm, Recent: recent, Error: failed}}
	if token.Kind == tokenString || token.Kind == tokenRaw {
		return r
	}
	source := req.Source
	if p.completionQuery != nil {
		source = p.completionQuery.Source
	}
	state := completionCandidates{req: req, token: token, p: p, source: source}
	if schema != nil {
		state.signal = schema.Signals[source]
	}
	cursor := max(token.Span.Start, min(req.Cursor, token.Span.End))
	if token.Kind != eof {
		state.prefix = string([]rune(req.Text)[token.Span.Start:cursor])
	}
	if token.Kind == tokenBracketKey {
		r.Replace.Start++
		if !token.Unclosed {
			r.Replace.End--
		}
		state.prefix = strings.TrimPrefix(state.prefix, string(token.Quote))
		quote := string(token.Quote)
		state.prefix = strings.ReplaceAll(state.prefix, quote+quote, quote)
	}
	for i, e := range p.expected {
		state.class = i
		state.candidates(e)
	}
	sort.SliceStable(state.items, func(i, j int) bool {
		a, b := state.items[i], state.items[j]
		if a.match != b.match {
			return a.match < b.match
		}
		if a.used != b.used {
			return !a.used
		}
		if a.boost != b.boost {
			return a.boost > b.boost
		}
		if a.class != b.class {
			return a.class < b.class
		}
		return a.count > b.count
	})
	seen := map[string]bool{}
	for _, candidate := range state.items {
		if seen[candidate.item.Label] {
			continue
		}
		seen[candidate.item.Label] = true
		r.Items = append(r.Items, candidate.item)
		if len(r.Items) == maxCompletionItems {
			break
		}
	}
	return r
}

func (s *completionCandidates) add(label, insert, kind, detail string, count int64, boost int) {
	match := 0
	prefix := strings.ToLower(s.prefix)
	lower := strings.ToLower(label)
	if prefix != "" && !strings.HasPrefix(lower, prefix) {
		if !strings.Contains(lower, prefix) {
			return
		}
		match = 1
	}
	if insert == "" {
		insert = label
	}
	class := s.class * 2
	if kind == "attribute" {
		class++
	}
	s.items = append(s.items, completionCandidate{item: Item{Label: label, Insert: insert, Kind: kind, Detail: detail}, class: class, match: match, count: count, boost: boost, used: s.p.completionUsed[lower]})
}

func (s *completionCandidates) words(kind string, words ...string) {
	for _, word := range words {
		s.add(word, word, kind, "", 0, 0)
	}
}

func (s *completionCandidates) candidates(e expect) {
	switch e.Class {
	case "source":
		s.words("keyword", panel.SignalNames()...)
	case "field":
		s.fields(e, false, "")
	case "bracket_key":
		s.fields(e, false, e.Scope)
	case "variable":
		for _, v := range s.req.Variables {
			s.add("$"+v.Name, "", "variable", "", 0, 0)
		}
	case "free_text":
		if strings.ContainsAny(s.prefix, ".$[") {
			return
		}
		column := "operation"
		if s.source == "logs" {
			column = "body"
		}
		// This hint follows real suggestions even when the prefix matches no field.
		s.items = append(s.items, completionCandidate{item: Item{Label: s.prefix, Insert: s.prefix, Kind: "value", Detail: "free text searches " + column}, match: 3, class: s.class})
	case "operator":
		if e.Scope == "term" {
			s.words("operator", "-", "(", "not")
			return
		}
		s.operators(e.Field)
	case "value":
		s.values(e)
	case "measure":
		s.measures()
	case "marg":
		if e.Func == "quantile" && e.Arg == 1 {
			s.words("value", "0.5", "0.9", "0.95", "0.99")
			return
		}
		if e.Func == "histogram" {
			s.fields(e, true, "")
			return
		}
		for _, spec := range panel.MeasureSpecs() {
			if spec.Name == e.Func && e.Arg < spec.Args {
				s.fields(e, spec.Numeric, "")
				return
			}
		}
	case "bucket":
		if s.req.Viz != "" && !slices.Contains([]string{"timeseries", "heatmap", "state_timeline", "log_patterns"}, s.req.Viz) {
			s.add("", "", "keyword", s.req.Viz+" panels have no bucket", 0, 0)
			return
		}
		s.words("bucket", panel.BucketNames()...)
	case "sort_key":
		s.sortKeys()
	case "integer":
		limit := 1000
		if s.req.Viz == "log_patterns" {
			limit = 50
		}
		if s.req.Viz == "health" || s.req.Viz == "service_map" {
			limit = 400
		}
		for _, n := range []int{10, 50, 100, 1000} {
			if n <= limit {
				s.words("value", fmt.Sprint(n))
			}
		}
	case "clause":
		switch e.Scope {
		case "measure":
			s.words("keyword", ",", "as", "by")
			if s.bucketAllowed() {
				s.words("keyword", "every")
			}
			s.words("keyword", "sort", "limit")
			if stage := s.p.completionQuery.Stage; stage != nil && len(stage.Measures) > 0 {
				_, share, _ := measureLeaves(stage.Measures[len(stage.Measures)-1].Expr)
				if !share {
					s.words("operator", "+", "-", "*", "/")
				}
			}
		case "stage":
			if s.p.completionQuery.Stage.Bucket == "" && s.bucketAllowed() {
				s.words("keyword", "every")
			}
			s.words("keyword", "sort", "limit")
		default:
			if s.req.Source == "" {
				s.words("keyword", "where", "|", "sort", "limit")
			}
		}
	}
}

func (s *completionCandidates) bucketAllowed() bool {
	return s.req.Viz == "" || slices.Contains([]string{"timeseries", "heatmap", "state_timeline", "log_patterns"}, s.req.Viz)
}

func (s *completionCandidates) fields(e expect, numeric bool, scope string) {
	if e.Func == "by" {
		maximum := 3
		switch s.req.Viz {
		case "timeseries", "state_timeline", "histogram", "log_patterns":
			maximum = 1
		case "bar", "scatter":
			maximum = 2
		case "stat", "gauge", "heatmap", "logs", "traces", "health", "service_map":
			maximum = 0
		}
		if e.Arg >= maximum {
			s.add("", "", "keyword", fmt.Sprintf("%s groups by at most %d field", s.req.Viz, maximum), 0, 0)
			return
		}
	}
	if scope == "" {
		fields, _ := panel.SignalColumns(s.source)
		for _, field := range fields {
			if field.Type == panel.TypeMap || e.Func == "by" && field.Type != panel.TypeString {
				continue
			}
			name := field.Name
			if name == "duration_ms" {
				name = "duration"
			}
			boost := 0
			if numeric && field.Type == panel.TypeNumber {
				boost = 2
			}
			s.add(name, name, "field", field.Description, 0, boost)
		}
	}
	for _, attribute := range s.signal.Attributes {
		attributeScope := attribute.Scope
		if attributeScope == "" {
			attributeScope = "attributes"
		}
		if scope != "" && attributeScope != scope {
			continue
		}
		label := attribute.Key
		var insert string
		if scope != "" {
			quote := string(s.token.Quote)
			insert = strings.ReplaceAll(attribute.Key, quote, quote+quote)
			if s.token.Unclosed {
				insert += quote + "]"
			}
		} else {
			label = printField(fieldName{Scope: attributeScope, Key: attribute.Key})
			insert = label
		}
		boost := 0
		if numeric && attribute.Type == "number" {
			boost = 2
		}
		if s.serviceBoost(attribute.Services) {
			boost++
		}
		s.add(label, insert, "attribute", attribute.Type, attribute.Count, boost)
	}
}

func (s *completionCandidates) serviceBoost(services string) bool {
	if services == "" || s.p.completionQuery == nil {
		return false
	}
	var has func(filterExpr) bool
	has = func(x filterExpr) bool {
		switch x := x.(type) {
		case *comparison:
			if x.Field.Key == "service" && x.Field.Scope == "" && (x.Op == ":" || x.Op == "=") && x.Value.Kind != variableValue {
				return slices.Contains(strings.Split(services, ", "), x.Value.Text)
			}
		case *andExpr:
			for _, term := range x.Terms {
				if has(term) {
					return true
				}
			}
		}
		return false
	}
	for _, x := range s.p.completionQuery.Where {
		if has(x) {
			return true
		}
	}
	return false
}

func (s *completionCandidates) operators(field *fieldName) {
	if field == nil {
		return
	}
	if field.Scope == "" {
		switch strings.ToLower(field.Key) {
		case "duration":
			s.words("operator", ">", ">=", "<", "<=", "between", ":")
			return
		case "severity":
			s.words("operator", ">", ">=", "<", "<=", ":")
			return
		}
		fields, _ := panel.SignalColumns(s.source)
		for _, f := range fields {
			if f.Name == field.Key && f.Type == panel.TypeTime {
				s.words("operator", "=", "!=", ">", ">=", "<", "<=")
				return
			}
		}
	}
	s.words("operator", ":", ">", ">=", "<", "<=", "!=", "~", "in", "not in", "between", "is null", "like")
}

func (s *completionCandidates) values(e expect) {
	if e.Field == nil {
		return
	}
	f := e.Field
	seen := map[string]bool{}
	vocabulary := true
	addValue := func(label, stored, insert string, count int64) {
		if seen[stored] {
			return
		}
		seen[stored] = true
		boost := 0
		if vocabulary {
			boost = 1
		}
		s.add(label, insert, "value", "", count, boost)
	}
	value := func(label, stored string, count int64) { addValue(label, stored, label, count) }
	sampledValue := func(label string, count int64) {
		addValue(label, label, printValue(literalValue{Kind: stringValue, Text: label}, *f, false), count)
	}
	if f.Scope == "" {
		if f.Key == "severity" {
			for _, band := range telemetry.SeverityBands {
				value(strings.ToLower(band), band, 0)
			}
			if isRange(e.Scope) || e.Scope == "between" {
				return
			}
		} else {
			aliases := panel.FieldAliases(f.Key)
			for _, label := range sortedCompletionKeys(aliases) {
				value(label, aliases[label], 0)
			}
		}
		vocabulary = false
		if f.Key == "duration" {
			for _, v := range []string{"500ms", "1s", "5s"} {
				value(v, v, 0)
			}
		}
		for _, column := range s.signal.Columns {
			if column.Name == f.Key {
				for _, v := range column.Values {
					sampledValue(v.Value, v.Count)
				}
			}
		}
		if f.Key == "name" {
			for _, metric := range s.signal.Metrics {
				sampledValue(metric.Name, metric.Count)
			}
		}
	}
	vocabulary = false
	for _, attribute := range s.signal.Attributes {
		scope := f.Scope
		if scope == "" {
			scope = "attributes"
		}
		if attribute.Key == f.Key && (attribute.Scope == scope || attribute.Scope == "" && scope == "attributes") && attribute.Type == "boolean" {
			value("true", "true", 0)
			value("false", "false", 0)
		}
	}
	if e.Scope != "in" {
		for _, v := range s.req.Variables {
			s.add("$"+v.Name, "", "variable", "", 0, 0)
		}
		value("*", "*", 0)
	}
}

func sortedCompletionKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (s *completionCandidates) measures() {
	for _, spec := range panel.MeasureSpecs() {
		if len(spec.Signals) > 0 && !slices.Contains(spec.Signals, s.source) {
			continue
		}
		insert := spec.Name + "("
		if spec.Args == 0 {
			insert += ")"
		}
		s.add(spec.Name, insert, "measure", "", 0, 0)
	}
	if s.source != "metrics" {
		s.add("count_if", "count_if(", "measure", "", 0, 0)
	}
	if (s.req.Viz == "" || s.req.Viz == "heatmap" || s.req.Viz == "histogram") && (s.source == "spans" || s.source == "metrics") {
		s.add("histogram", "histogram(", "measure", "", 0, 0)
	}
}

func (s *completionCandidates) sortKeys() {
	if q := s.p.completionQuery; q != nil && q.Stage != nil {
		// The resolver owns aliases, including repeated calls whose argument's
		// resolved spelling forms part of the alias (duration -> duration_ms).
		r := resolver{vocab: currentVocabulary(), out: &resolved{Query: q}}
		for _, measure := range q.Stage.Measures {
			r.measure(measure.Expr)
		}
		for _, measure := range q.Stage.Measures {
			alias := measureAlias(measure, q.Stage.Measures)
			if alias != "" {
				s.words("field", alias, "+"+alias)
			}
		}
		return
	}
	switch s.req.Viz {
	case "traces":
		s.words("field", "duration", "+start")
		s.add("errors", "errors", "field", "filters to errors, orders by duration", 0, 0)
	case "log_patterns":
		s.words("field", "count")
	default:
		if s.source == "logs" {
			s.words("field", "time", "+time")
		} else if s.source == "spans" {
			s.words("field", "duration", "+start")
			s.add("errors", "errors", "field", "filters to errors, orders by duration", 0, 0)
		}
	}
}
