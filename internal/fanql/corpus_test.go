package fanql

import (
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/labstack/fanout/internal/query"
)

// Each JSON block is an independently pinned input/output fixture. There is no
// update mode: changes to this oracle require review, including spans/fix-its.
type corpusFixture struct {
	Text         string         `json:"text"`
	Viz          string         `json:"viz,omitempty"`
	Query        *query.Spec    `json:"query,omitempty"`
	Problems     query.Problems `json:"problems,omitempty"`
	GateProblems bool           `json:"gate_problems,omitempty"`
}

func readCorpus(t testing.TB) []corpusFixture {
	t.Helper()
	f, err := os.Open("testdata/corpus.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	var out []corpusFixture
	seen := map[string]bool{}
	for {
		var fixture corpusFixture
		if err := d.Decode(&fixture); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		key := fixture.Text + "\x00" + fixture.Viz
		if seen[key] {
			t.Fatalf("duplicate corpus input %q", fixture.Text)
		}
		seen[key] = true
		if (fixture.Query == nil) == (len(fixture.Problems) == 0) {
			t.Fatalf("fixture needs exactly one of query/problems: %+v", fixture)
		}
		out = append(out, fixture)
	}
	if len(out) < 200 {
		t.Fatalf("corpus has only %d fixtures", len(out))
	}
	return out
}

func TestCorpus(t *testing.T) {
	positives, negatives := 0, 0
	for _, f := range readCorpus(t) {
		t.Run(f.Text+"/"+f.Viz, func(t *testing.T) {
			c, ps := Compile(f.Text, Options{Viz: f.Viz})
			if f.Query == nil {
				negatives++
				if c != nil || !reflect.DeepEqual(ps, f.Problems) {
					t.Fatalf("got %+v %#v; want %#v", c, ps, f.Problems)
				}
			} else {
				positives++
				if len(ps) != 0 || c == nil {
					t.Fatalf("compile: %+v", ps)
				}
				a, _ := json.Marshal(c.Query)
				b, _ := json.Marshal(f.Query)
				if string(a) != string(b) {
					t.Fatalf("got %s; want %s", a, b)
				}
			}
		})
	}
	if positives < 100 || negatives < 60 {
		t.Fatalf("need broad positive and negative coverage, got %d/%d", positives, negatives)
	}
}

func TestCanonicalFreeze(t *testing.T) {
	path := "testdata/canonical.golden"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	clean := map[string]bool{}
	for _, f := range readCorpus(t) {
		if f.Query == nil {
			continue
		}
		clean[f.Text] = true
		c, ps := Compile(f.Text, Options{Viz: f.Viz})
		if len(ps) != 0 {
			t.Fatal(f.Text, ps)
		}
		if want, ok := golden[f.Text]; !ok || c.Canonical != want {
			t.Fatalf("FanQL canonical changed for %q: %q; pinned %q", f.Text, c.Canonical, want)
		}
	}
	if len(golden) != len(clean) {
		t.Fatalf("golden has %d entries, corpus has %d clean texts", len(golden), len(clean))
	}
}

func TestCorpusIncludesVizContracts(t *testing.T) {
	fixtures := readCorpus(t)
	for _, context := range []struct{ text, viz string }{
		{"spans sort +duration", "traces"}, {"logs sort duration", "logs"},
		{"logs | count() by body_template sort rate", "log_patterns"},
		{"spans where status:error | count() by service", "table"},
		{"spans sort duration", "traces"}, {"logs sort +time", "logs"},
		{"spans | histogram(duration) by service", "histogram"},
		{"spans | count() by service every 1m", "timeseries"},
	} {
		found := false
		for _, fixture := range fixtures {
			if fixture.Text == context.text && fixture.Viz == context.viz {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing viz fixture %q (%s)", context.text, context.viz)
		}
	}
}

func TestPrintParsePrintCorpus(t *testing.T) {
	for _, f := range readCorpus(t) {
		if f.Query == nil {
			continue
		}
		a, ps := Compile(f.Text, Options{Viz: f.Viz})
		if len(ps) != 0 {
			t.Fatal(f.Text, ps)
		}
		b, ps := Compile(a.Canonical, Options{Viz: f.Viz})
		if len(ps) != 0 {
			t.Fatalf("%q -> %q: %+v", f.Text, a.Canonical, ps)
		}
		if a.Canonical != b.Canonical || !reflect.DeepEqual(a.Query, b.Query) {
			t.Fatalf("not a fixed point: %q -> %q -> %+v", f.Text, a.Canonical, b)
		}
	}
}
