package telemetry

import (
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/zeebo/xxh3"
)

// MetricSeriesID hashes the OTLP series identity, independent of point value,
// kind, temporality and time. Call after decoding the complete identity.
// Ingest hashes every point, so the encoding is built on the stack.
func MetricSeriesID(m Metric) uint64 {
	var buffer [512]byte
	return xxh3.Hash(metricSeriesEncoding(buffer[:0], &m))
}

// metricSeriesEncoding appends the series identity to b.
func metricSeriesEncoding(b []byte, m *Metric) []byte {
	for _, s := range [...]string{m.Namespace, m.ServiceName, m.Name, m.Unit, m.ScopeName, m.ScopeVersion} {
		b = appendMetricIdentityString(b, s)
	}
	b = appendMetricIdentityValue(b, m.Attributes)
	return appendMetricIdentityValue(b, m.Resource)
}

// Type tags and lengths make field and container boundaries unambiguous.
// Integers and doubles deliberately have different tags, including 1 and 1.0.
func appendMetricIdentityValue(b []byte, v any) []byte {
	switch v := v.(type) {
	case nil:
		return append(b, 'n')
	case string:
		return appendMetricIdentityString(b, v)
	case bool:
		if v {
			return append(b, 'b', 1)
		}
		return append(b, 'b', 0)
	case int:
		return binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(v))
	case int32:
		return binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(v))
	case int64:
		return binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(v))
	case float64:
		return binary.LittleEndian.AppendUint64(append(b, 'd'), math.Float64bits(v))
	case []byte:
		b = binary.LittleEndian.AppendUint64(append(b, 'y'), uint64(len(v)))
		return append(b, v...)
	case []any:
		b = binary.LittleEndian.AppendUint64(append(b, 'a'), uint64(len(v)))
		for _, child := range v {
			b = appendMetricIdentityValue(b, child)
		}
		return b
	case map[string]any:
		var stack [16]string
		keys := stack[:0]
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		b = binary.LittleEndian.AppendUint64(append(b, 'm'), uint64(len(keys)))
		for _, k := range keys {
			b = appendMetricIdentityString(b, k)
			b = appendMetricIdentityValue(b, v[k])
		}
		return b
	default:
		panic(fmt.Sprintf("unsupported canonical metric attribute %T", v))
	}
}

func appendMetricIdentityString(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint64(append(b, 's'), uint64(len(s)))
	return append(b, s...)
}
