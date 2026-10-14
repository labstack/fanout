import { afterEach, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { pathToFileURL } from "node:url";
import { main, prepareEnvironment } from "./run-fanout.mjs";

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

function fixture(): string {
  const root = mkdtempSync(join(tmpdir(), "fanout-env-"));
  roots.push(root);
  writeFileSync(join(root, ".env.example"), `# server.addr; string
FANOUT_ADDR=:7520

# ai.model; string
FANOUT_AI_MODEL=

# storage.data_dir; string
FANOUT_DATA_DIR=./data

# auth.code_secret; string; secret
FANOUT_AUTH_CODE_SECRET=

# ai.anthropic.api_key; string; secret
ANTHROPIC_API_KEY=
`);
  writeFileSync(join(root, ".env"), "FANOUT_ADDR=127.0.0.1:7520\nFANOUT_AI_MODEL=base\n");
  return root;
}

test("local dotenv defaults, personal overrides, and explicit injection have ordered precedence", () => {
  const root = fixture();
  writeFileSync(join(root, ".env.local"), "FANOUT_ADDR=127.0.0.1:9000\nFANOUT_AI_MODEL=personal\n");
  const env = prepareEnvironment(root, "local", { FANOUT_ADDR: "", FANOUT_AI_MODEL: "injected", FANOUT_AUTH_CODE_SECRET: "synthetic-only", PATH: "/synthetic/path" });
  expect(env.FANOUT_ADDR).toBe("127.0.0.1:9000");
  expect(env.FANOUT_AI_MODEL).toBe("injected");
  expect(env.FANOUT_AUTH_CODE_SECRET).toBe("synthetic-only");
  expect(env.PATH).toBe("/synthetic/path");
});

test("production excludes personal files and requires its declared override file", () => {
  const root = fixture();
  writeFileSync(join(root, ".env.local"), "ANTHROPIC_API_KEY=personal-secret\n");
  writeFileSync(join(root, ".env.production.local"), "FANOUT_AI_MODEL=personal-production\n");
  expect(() => prepareEnvironment(root, "production", {})).toThrow("Cannot read required");
  writeFileSync(join(root, ".env.production"), "FANOUT_ADDR=:7520\nFANOUT_AI_MODEL=production\n");
  const env = prepareEnvironment(root, "production", {});
  expect(env.FANOUT_ADDR).toBe(":7520");
  expect(env.FANOUT_AI_MODEL).toBe("production");
  expect(env.ANTHROPIC_API_KEY).toBeUndefined();
  expect(prepareEnvironment(root, "production", { FANOUT_AI_MODEL: "deployment" }).FANOUT_AI_MODEL).toBe("deployment");
});

test("dotenv parsing preserves dollars, quotes, whitespace, and multiline values without shell execution", () => {
  const root = fixture();
  writeFileSync(join(root, ".env.local"), `FANOUT_DATA_DIR='  $HOME $(printf unsafe) "quoted"  '
FANOUT_AI_MODEL="line one
line two"
`);
  const env = prepareEnvironment(root, "local", {});
  expect(env.FANOUT_DATA_DIR).toBe('  $HOME $(printf unsafe) "quoted"  ');
  expect(env.FANOUT_AI_MODEL).toBe("line one\nline two");
  // Verify the real Node runtime as well as Bun's test-runner implementation.
  const result = Bun.spawnSync(["node", "--input-type=module", "-e",
    "const {prepareEnvironment}=await import(process.argv[2]); process.stdout.write(JSON.stringify(prepareEnvironment(process.argv[1], 'local', {})));",
    root, pathToFileURL(join(import.meta.dir, "run-fanout.mjs")).href], { cwd: root });
  expect(result.exitCode).toBe(0);
  expect(JSON.parse(result.stdout.toString())).toEqual(env);
});

test("public and personal dotenv files reject secrets, retired names, and unresolved references without leaking values", () => {
  for (const assignment of [
    "ANTHROPIC_API_KEY=synthetic-private-value",
    "FANOUT_AUTH_CODE_SECRET=",
    "FANOUT_AI_API_KEY=synthetic-private-value",
    "FANOUT_AI_BASE_URL=https://example.com/synthetic-private-value",
    "AI_API_KEY=synthetic-private-value",
    "FANOUT_AI_MODEL=op://synthetic-private-value",
  ]) {
    for (const file of [".env", ".env.local"]) {
      const root = fixture();
      writeFileSync(join(root, file), assignment+"\n");
      let message = "";
      try { prepareEnvironment(root, "local", {}); } catch (error) { message = (error as Error).message; }
      expect(message).not.toBe("");
      expect(message).not.toContain("synthetic-private-value");
    }
  }
});

test("missing defaults and unsupported environments fail rather than fall back", () => {
  const root = fixture();
  rmSync(join(root, ".env"));
  expect(() => prepareEnvironment(root, "local", {})).toThrow("Cannot read required");
  expect(() => prepareEnvironment(root, "staging" as "local", {})).toThrow("Environment must");
});

test("launcher requires an explicit environment and command", async () => {
  await expect(main([])).rejects.toThrow("Usage:");
  await expect(main(["--environment", "local", "--"])).rejects.toThrow("Usage:");
});
