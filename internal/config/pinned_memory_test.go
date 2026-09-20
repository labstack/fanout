package config

import "testing"

// Pinning storage.duckdb.memory skips detection entirely, so nothing compares
// the value to the machine. Both production deployments pinned 4GB on 8 GiB
// hosts and were OOM-killed repeatedly: DuckDB's budget is not the process's
// budget, and the Go runtime, the ingest appender and compaction's merges all
// sit on top of it. The operator keeps the last word -- this warns, it does not
// refuse -- but a value that cannot fit should not pass in silence.
func TestPinnedMemoryConcern(t *testing.T) {
	const eightGB = 8 << 30
	tests := []struct {
		name      string
		pinned    string
		detected  uint64
		wantWarns bool
	}{
		{name: "a pin inside the automatic share is fine", pinned: "4GB", detected: 16 << 30},
		{name: "the share the machine would have chosen is fine", pinned: "4800MB", detected: eightGB},
		{name: "half the machine is fine", pinned: "4GB", detected: eightGB},
		{name: "most of the machine leaves nothing for the runtime", pinned: "7GB", detected: eightGB, wantWarns: true},
		{name: "more than the machine is always wrong", pinned: "16GB", detected: eightGB, wantWarns: true},
		{name: "gibibytes parse too", pinned: "7GiB", detected: eightGB, wantWarns: true},
		{name: "nothing pinned, nothing to say", pinned: "", detected: eightGB},
		{name: "undetectable machine cannot be judged", pinned: "7GB", detected: 0},
		{name: "unparseable pin is not silently treated as zero", pinned: "lots", detected: eightGB, wantWarns: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := pinnedMemoryConcern(test.pinned, test.detected)
			if gotWarns := got != ""; gotWarns != test.wantWarns {
				t.Errorf("pinnedMemoryConcern(%q, %d) = %q; warning expected: %v",
					test.pinned, test.detected, got, test.wantWarns)
			}
		})
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{in: "4GB", want: 4 << 30, ok: true},
		{in: "4GiB", want: 4 << 30, ok: true},
		{in: "512MB", want: 512 << 20, ok: true},
		{in: "8192", want: 8192, ok: true},
		{in: "6.4GiB", want: 6871947673, ok: true},
		{in: " 2 GB ", want: 2 << 30, ok: true},
		{in: "", ok: false},
		{in: "lots", ok: false},
		{in: "-4GB", ok: false},
	}
	for _, test := range tests {
		t.Run(test.in, func(t *testing.T) {
			got, ok := parseByteSize(test.in)
			if ok != test.ok {
				t.Fatalf("parseByteSize(%q) ok = %v, want %v", test.in, ok, test.ok)
			}
			if ok && got != test.want {
				t.Errorf("parseByteSize(%q) = %d, want %d", test.in, got, test.want)
			}
		})
	}
}
