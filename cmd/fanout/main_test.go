package main

import (
	"errors"
	"flag"
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
			path, showVersion, err := parseCommandLine(test.args)
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
	_, _, err := parseCommandLine([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
}
