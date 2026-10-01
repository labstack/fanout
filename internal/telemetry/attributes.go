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
