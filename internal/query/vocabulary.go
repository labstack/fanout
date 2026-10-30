package query

import (
	"maps"
	"slices"
)

// FieldVocabulary is the closed value vocabulary for a catalog field.
type FieldVocabulary struct {
	Signal  string
	Order   []string
	Aliases map[string]string
}

var fieldVocabularies = map[string]FieldVocabulary{
	"status": {"spans", []string{"error", "ok", "unset"}, map[string]string{"error": "STATUS_CODE_ERROR", "ok": "STATUS_CODE_OK", "unset": "STATUS_CODE_UNSET"}},
	"kind":   {"spans", []string{"server", "client", "internal", "producer", "consumer", "unspecified"}, map[string]string{"server": "SPAN_KIND_SERVER", "client": "SPAN_KIND_CLIENT", "internal": "SPAN_KIND_INTERNAL", "producer": "SPAN_KIND_PRODUCER", "consumer": "SPAN_KIND_CONSUMER", "unspecified": "SPAN_KIND_UNSPECIFIED"}},
	"type":   {"metrics", []string{"gauge", "counter", "updown", "histogram", "summary"}, map[string]string{"gauge": "gauge", "counter": "counter", "updown": "updown", "histogram": "histogram", "summary": "summary"}},
}

// FieldVocabularies returns isolated snapshots, shared by all query frontends.
func FieldVocabularies() map[string]FieldVocabulary {
	out := make(map[string]FieldVocabulary, len(fieldVocabularies))
	for field, v := range fieldVocabularies {
		v.Aliases = maps.Clone(v.Aliases)
		v.Order = slices.Clone(v.Order)
		out[field] = v
	}
	return out
}

// FieldAliases returns an isolated snapshot of a field's aliases.
func FieldAliases(field string) map[string]string {
	return maps.Clone(fieldVocabularies[field].Aliases)
}
