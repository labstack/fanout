package intelligence

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every statement this package issues goes through a validator that rejects
// SQL comments outright (internal/query/sql.go: "SQL comments (--) are not
// allowed"). A "--" inside a query string therefore fails at runtime, on a
// background goroutine, as a logged error nobody is watching -- the anomaly
// detector silently stopped reporting latency for half an hour that way.
//
// The compiler cannot catch it and no unit test here executes these strings,
// so this reads the source instead. Rationale belongs in Go comments.
func TestNoSQLCommentsInQueryStrings(t *testing.T) {
	source, err := os.ReadFile("detector.go")
	if err != nil {
		t.Fatal(err)
	}
	// Raw-string literals are where the SQL lives.
	literals := regexp.MustCompile("(?s)`[^`]*`").FindAllString(string(source), -1)
	if len(literals) == 0 {
		t.Fatal("no raw string literals found; this guard is no longer looking at the right thing")
	}
	found := 0
	for _, literal := range literals {
		if !strings.Contains(strings.ToUpper(literal), "SELECT") {
			continue
		}
		found++
		for i, line := range strings.Split(literal, "\n") {
			if idx := strings.Index(line, "--"); idx >= 0 {
				t.Errorf("SQL comment in a query string (line %d of a literal): %q\n"+
					"the validator rejects these at runtime; put the explanation in a Go comment",
					i+1, strings.TrimSpace(line))
			}
		}
	}
	if found == 0 {
		t.Fatal("no SELECT literals found; this guard is no longer looking at the right thing")
	}
	t.Logf("checked %d SQL literals", found)
}

// DuckDB's FLOAT is single precision, so the driver returns float32 and the
// `row["x"].(float64)` every caller here writes fails -- silently yielding 0.0.
// internal/query now widens float32 on the way out, which disarms the trap, but
// a rate or a count has no business being single precision in the first place
// and the next reader should not have to know about the widening to trust it.
func TestNoFloatCastsInQueryStrings(t *testing.T) {
	source, err := os.ReadFile("detector.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(source), "\n") {
		if strings.Contains(strings.ToUpper(line), "::FLOAT") {
			t.Errorf("detector.go:%d casts to FLOAT (single precision): %q\nuse ::DOUBLE", i+1, strings.TrimSpace(line))
		}
	}
}
