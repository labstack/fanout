package panel

import "testing"

func TestRedactPathsKeepsArithmetic(t *testing.T) {
	for in, want := range map[string]string{
		`IO Error: No files found that match the pattern "/var/lib/fanout/data/telemetry/parquet/batches/*.batch/spans.parquet"`: `IO Error: No files found that match the pattern <path>`,
		`cannot open /opt/fanout/data/x.parquet: denied`: `cannot open <path>: denied`,
		`Binder Error: duration_ms/1000 is not a column`: `Binder Error: duration_ms/1000 is not a column`,
		`C:\data\x.parquet missing`:                      `<path> missing`,
	} {
		if got := RedactPaths(in); got != want {
			t.Errorf("RedactPaths(%q) = %q, want %q", in, got, want)
		}
	}
}
