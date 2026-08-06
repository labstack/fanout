package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseCommandLine(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPath    string
		wantVersion bool
		wantErr     bool
	}{
		{name: "server defaults", args: nil},
		{name: "config", args: []string{"--config", "/etc/fanout.yaml"}, wantPath: "/etc/fanout.yaml"},
		{name: "version command", args: []string{"version"}, wantVersion: true},
		{name: "version flag", args: []string{"--version"}, wantVersion: true},
		{name: "short version flag", args: []string{"-v"}, wantVersion: true},
		{name: "unexpected argument", args: []string{"serve"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, showVersion, err := parseCommandLine(test.args, io.Discard)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if path != test.wantPath || showVersion != test.wantVersion {
				t.Fatalf("result = (%q, %v), want (%q, %v)", path, showVersion, test.wantPath, test.wantVersion)
			}
		})
	}
}

func TestParseCommandLineHelp(t *testing.T) {
	_, _, err := parseCommandLine([]string{"--help"}, io.Discard)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
}

func TestParseCommandLinePrintsOneErrorAndUsage(t *testing.T) {
	var output bytes.Buffer
	_, _, err := parseCommandLine([]string{"serve"}, &output)
	if err == nil {
		t.Fatal("expected unexpected-argument error")
	}
	if got := strings.Count(output.String(), "unexpected arguments: serve"); got != 1 {
		t.Fatalf("error appeared %d times in %q", got, output.String())
	}
	if !strings.Contains(output.String(), "Usage of fanout") {
		t.Fatalf("output does not include usage: %q", output.String())
	}
}
