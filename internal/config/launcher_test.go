package config

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGeneratedLauncherEnvironmentPreservesYAML(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env", ".env.production", ".env.example"} {
		body, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "fanout.yaml")
	if err := os.WriteFile(path, []byte("storage:\n  data_dir: ./yaml-data\n  retention_days: 17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher, err := filepath.Abs("../../scripts/run-fanout.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"local", "production"} {
		t.Run(environment, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", "--input-type=module", "-e", `
const { pathToFileURL } = await import('node:url');
const { prepareEnvironment } = await import(pathToFileURL(process.argv[3]).href);
process.stdout.write(JSON.stringify(prepareEnvironment(process.argv[1], process.argv[2], {
  FANOUT_AUTH_CODE_SECRET: '0123456789abcdef0123456789abcdef'
})));`, root, environment, launcher)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Node launcher: %v\n%s", err, output)
			}
			var values map[string]string
			if err := json.Unmarshal(output, &values); err != nil {
				t.Fatal(err)
			}
			var env []string
			for name, value := range values {
				env = append(env, name+"="+value)
			}
			cfg, err := Load(LoadOptions{Path: path, Environ: env})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DataDir != "./yaml-data" || cfg.RetentionDays != 17 {
				t.Fatal("generated launcher defaults overrode YAML settings")
			}
			wantAddr := "127.0.0.1:7520"
			if environment == "production" {
				wantAddr = ":7520"
			}
			if cfg.Addr != wantAddr {
				t.Fatalf("listener = %q, want %q", cfg.Addr, wantAddr)
			}
		})
	}
}
