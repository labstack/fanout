package telemetry

import "testing"

func TestNormalizeSeverityNumberWins(t *testing.T) {
	for _, tc := range []struct {
		text   string
		number int32
		want   string
	}{
		{"WARN2", 14, "WARN"}, {"ERROR", 9, "INFO"}, {"FATAL", 0, "FATAL"},
		{"INFO2", 0, "INFO"}, {"warning", 0, "WARN"}, {"custom", 0, "CUSTOM"},
		{"", 0, "UNSPECIFIED"}, {"anything", 21, "FATAL"}, {"anything", 1, "TRACE"},
	} {
		if got := NormalizeSeverity(tc.text, tc.number); got != tc.want {
			t.Errorf("NormalizeSeverity(%q, %d) = %q, want %q", tc.text, tc.number, got, tc.want)
		}
	}
}

func TestSeverityRankOrder(t *testing.T) {
	for i, band := range []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"} {
		if rank, ok := SeverityRank(band); !ok || rank != i {
			t.Errorf("SeverityRank(%q) = %d, %v", band, rank, ok)
		}
	}
	for _, band := range []string{"UNSPECIFIED", "CUSTOM"} {
		if _, ok := SeverityRank(band); ok {
			t.Errorf("SeverityRank(%q) succeeded", band)
		}
	}
}

func TestSeverityAliasesMatchSeverityFromText(t *testing.T) {
	aliases := SeverityAliases()
	for _, a := range []string{"warning", "critical", "crit", "emerg", "emergency", "alert", "panic", "err", "severe", "exception", "information", "informational", "notice", "dbg", "fine", "verbose", "finest"} {
		if _, ok := aliases[a]; !ok {
			t.Errorf("missing alias %s", a)
		}
	}
	for alias, band := range aliases {
		if got := SeverityFromText(alias); got != band {
			t.Errorf("SeverityFromText(%q) = %q, want %q", alias, got, band)
		}
	}
	aliases["warning"] = "BROKEN"
	if SeverityAliases()["warning"] != "WARN" {
		t.Fatal("accessor exposes mutable table")
	}
}
