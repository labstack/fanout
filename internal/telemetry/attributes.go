package telemetry

// ValueBytes accounts for owned attribute payloads and container overhead
// without allocating another serialized copy while ingest is backpressured.
func ValueBytes(value any) int {
	switch value := value.(type) {
	case nil:
		return 0
	case string:
		return len(value) + 16
	case []byte:
		return len(value) + 24
	case map[string]any:
		total := 48
		for key, child := range value {
			total += 64 + len(key) + ValueBytes(child)
		}
		return total
	case []any:
		total := 24 + 16*len(value)
		for _, child := range value {
			total += ValueBytes(child)
		}
		return total
	default:
		return 16
	}
}

// ListBytes accounts for a span's events and links like ValueBytes does for
// maps, without serializing them.
func (s *Span) ListBytes() int {
	total := 48 * (len(s.Events) + len(s.Links))
	for _, e := range s.Events {
		total += len(e.Name) + ValueBytes(e.Attributes)
	}
	for _, l := range s.Links {
		total += len(l.TraceID) + len(l.SpanID) + len(l.TraceState) + ValueBytes(l.Attributes)
	}
	return total
}

// ListBytes accounts for a metric point's buckets, quantiles and exemplars.
func (m *Metric) ListBytes() int {
	total := 8 * (len(m.Bounds) + len(m.Counts) + len(m.PositiveCounts) + len(m.NegativeCounts) + len(m.Quantiles) + len(m.QuantileValues))
	for _, e := range m.Exemplars {
		total += 64 + len(e.TraceID) + len(e.SpanID) + ValueBytes(e.Attributes)
	}
	return total
}
