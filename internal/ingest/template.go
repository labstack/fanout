package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"log/slog"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"unicode/utf8"
)

// templateBodyLimit is how much of a body a template is derived from.
const templateBodyLimit = 500

var (
	reUUID      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	reIPv4      = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?\b`)
	reEmail     = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	reHexLong   = regexp.MustCompile(`\b0x[0-9a-fA-F]{4,}\b|\b[0-9a-fA-F]{8,}\b`)
	rePath      = regexp.MustCompile(`(/[a-zA-Z0-9._-]+){2,}(\?[^\s"]*)?`)
	reQuoted    = regexp.MustCompile(`"[^"]{1,200}"`)
	reNumber    = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
)

// normalizeText runs all 8 regexes in order against a plain-text log body,
// replacing variable content with typed placeholders.
func normalizeText(body string) string {
	body = reUUID.ReplaceAllString(body, "<uuid>")
	body = reTimestamp.ReplaceAllString(body, "<time>")
	body = reIPv4.ReplaceAllString(body, "<ip>")
	body = reEmail.ReplaceAllString(body, "<email>")
	body = reHexLong.ReplaceAllString(body, "<hex>")
	body = rePath.ReplaceAllString(body, "<path>")
	body = reQuoted.ReplaceAllString(body, "<str>")
	body = reNumber.ReplaceAllString(body, "<num>")
	return body
}

// truncateUTF8 truncates s to at most maxBytes bytes without splitting a
// multi-byte rune.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes until we find a rune-start boundary.
	b := maxBytes
	for b > 0 && !utf8.RuneStart(s[b]) {
		b--
	}
	return s[:b]
}

// safeNormalizeTemplate wraps normalizeTemplate with panic recovery so a
// single bad log line cannot crash the entire ingest pipeline.
func safeNormalizeTemplate(body string) (tmpl string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("normalizeTemplate panic", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			tmpl = ""
		}
	}()
	return normalizeTemplate(body)
}

// templateCache memoizes safeNormalizeTemplate by body. A log stream repeats
// a small set of bodies, and the template depends only on the body's first
// 500 bytes, so under load most rows skip the eight regex passes, which cost
// about as much CPU as encoding the row. The cache is direct-mapped: a body
// hashes to one slot per shard and replaces whatever that slot held, which
// bounds memory without an eviction list. Misses cost one hash and one
// mutex per shard; a unique-body stream pays the regex passes as before.
type templateCache struct {
	seed   maphash.Seed
	shards [templateCacheShards]templateShard
}

type templateShard struct {
	mu    sync.Mutex
	slots [templateCacheSlots]templateEntry
}

type templateEntry struct {
	body, template string
}

const (
	templateCacheShards = 64
	templateCacheSlots  = 256
)

var templates = newTemplateCache()

func newTemplateCache() *templateCache { return &templateCache{seed: maphash.MakeSeed()} }

// template returns the body's template, from the cache when the same body
// was seen recently.
func (c *templateCache) template(body string) string {
	body = truncateUTF8(body, templateBodyLimit)
	h := maphash.String(c.seed, body)
	shard := &c.shards[h%templateCacheShards]
	slot := &shard.slots[(h/templateCacheShards)%templateCacheSlots]
	shard.mu.Lock()
	if slot.body == body {
		tmpl := slot.template
		shard.mu.Unlock()
		return tmpl
	}
	shard.mu.Unlock()
	tmpl := safeNormalizeTemplate(body)
	// The truncated body, and a template that matched nothing, share memory
	// with the request's full body. Copies keep a slot at 500 bytes instead of
	// pinning a body of up to the request limit.
	shard.mu.Lock()
	slot.body, slot.template = strings.Clone(body), strings.Clone(tmpl)
	shard.mu.Unlock()
	return tmpl
}

// normalizeTemplate is the entry point. It truncates to 500 bytes (UTF-8
// safe), detects JSON by leading '{', and dispatches accordingly.
func normalizeTemplate(body string) string {
	body = truncateUTF8(body, templateBodyLimit)
	if len(body) > 0 && body[0] == '{' {
		return normalizeJSON(body)
	}
	return normalizeText(body)
}

// normalizeJSON unmarshals, walks values, and re-marshals. Falls back to
// normalizeText on invalid JSON.
func normalizeJSON(body string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return normalizeText(body)
	}
	normalized := normalizeJSONValues(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalized); err != nil {
		slog.Warn("normalizeJSON: re-marshal failed after successful unmarshal", "err", err)
		return normalizeText(body)
	}
	// json.Encoder.Encode appends a trailing newline; trim it.
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// normalizeJSONValues recursively normalizes JSON values:
//   - string → normalizeText applied
//   - float64 → "<num>"
//   - bool/nil → preserved
//   - map/slice → recursed
func normalizeJSONValues(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return normalizeText(val)
	case float64:
		return "<num>"
	case bool:
		return val
	case nil:
		return nil
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, child := range val {
			out[k] = normalizeJSONValues(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, child := range val {
			out[i] = normalizeJSONValues(child)
		}
		return out
	default:
		return val
	}
}
