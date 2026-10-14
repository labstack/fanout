import { afterEach, expect, test } from "bun:test";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawn } from "node:child_process";
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
FANOUT_ADDR=

# ai.model; string
FANOUT_AI_MODEL=

# storage.data_dir; string
FANOUT_DATA_DIR=

# auth.code_secret; string; secret
FANOUT_AUTH_CODE_SECRET=

# ai.anthropic.api_key; string; secret
ANTHROPIC_API_KEY=
`);
  writeFileSync(join(root, ".env"), "FANOUT_ADDR=127.0.0.1:7520\n");
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
  writeFileSync(join(root, ".env.production"), "FANOUT_ADDR=:7520\n");
  const env = prepareEnvironment(root, "production", {});
  expect(env.FANOUT_ADDR).toBe(":7520");
  expect(env.FANOUT_AI_MODEL).toBeUndefined();
  expect(env.ANTHROPIC_API_KEY).toBeUndefined();
  expect(prepareEnvironment(root, "production", { FANOUT_AI_MODEL: "deployment" }).FANOUT_AI_MODEL).toBe("deployment");
});

test("CRLF generated inventory preserves secret classification for local and production preparation", () => {
  const inventory = readFileSync(join(import.meta.dir, "../.env.example"), "utf8").replace(/\r?\n/g, "\r\n");
  for (const environment of ["local", "production"] as const) {
    const root = fixture();
    writeFileSync(join(root, ".env.example"), inventory);
    writeFileSync(join(root, ".env.production"), "FANOUT_ADDR=:7520\n");
    const injected = { FANOUT_AUTH_CODE_SECRET: "synthetic-only", ANTHROPIC_API_KEY: "synthetic-only" };
    const env = prepareEnvironment(root, environment, injected);
    expect(env.FANOUT_ADDR).toBe(environment === "local" ? "127.0.0.1:7520" : ":7520");
    expect(env.FANOUT_AUTH_CODE_SECRET).toBe(injected.FANOUT_AUTH_CODE_SECRET);
    expect(env.ANTHROPIC_API_KEY).toBe(injected.ANTHROPIC_API_KEY);
    const file = environment === "local" ? ".env.local" : ".env.production";
    for (const name of Object.keys(injected)) {
      writeFileSync(join(root, file), `${name}=synthetic-only\n`);
      expect(() => prepareEnvironment(root, environment, {})).toThrow(`Secret setting ${name} belongs in runtime injection, not ${file}`);
    }
  }
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

test("secret classification uses only the strict first-line schema header", () => {
  const root = fixture();
  writeFileSync(join(root, ".env.example"), `# ai.model; string
# documentation; string; secret
FANOUT_AI_MODEL=

# Invalid-Key; string; secret
FANOUT_DATA_DIR=

# auth.code_secret; string; secret
# An ordinary doc line cannot remove the secret marker.
FANOUT_AUTH_CODE_SECRET=
`);
  writeFileSync(join(root, ".env"), "FANOUT_AI_MODEL=public\nFANOUT_DATA_DIR=./public\n");
  expect(prepareEnvironment(root, "local", {}).FANOUT_AI_MODEL).toBe("public");
  expect(prepareEnvironment(root, "local", {}).FANOUT_DATA_DIR).toBe("./public");
  writeFileSync(join(root, ".env"), "FANOUT_AUTH_CODE_SECRET=synthetic-only\n");
  expect(() => prepareEnvironment(root, "local", {})).toThrow("Secret setting FANOUT_AUTH_CODE_SECRET");
});

function launchFixture(childCode: string) {
  const root = fixture();
  mkdirSync(join(root, "scripts"));
  copyFileSync(join(import.meta.dir, "run-fanout.mjs"), join(root, "scripts/run-fanout.mjs"));
  const harness = `
import { main } from './scripts/run-fanout.mjs';
const signals = ['SIGINT', 'SIGTERM', 'SIGHUP'];
const before = signals.map(signal => process.listenerCount(signal));
const code = await main(['--environment', 'local', '--', process.execPath, '-e', process.argv[2]]);
console.log('cleanup:' + signals.every((signal, index) => process.listenerCount(signal) === before[index]));
process.exitCode = code;
`;
  writeFileSync(join(root, "harness.mjs"), harness);
  return spawn("node", [join(root, "harness.mjs"), childCode], { env: { PATH: process.env.PATH }, stdio: ["ignore", "pipe", "pipe"] });
}

test("launcher forwards repeated SIGINT, SIGTERM, and SIGHUP until exit and removes handlers", async () => {
  const signals = ["SIGINT", "SIGTERM", "SIGHUP", "SIGINT", "SIGTERM", "SIGHUP"] as const;
  const launcher = launchFixture(`
let count = 0;
setTimeout(() => process.exit(1), 10000);
for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) process.on(signal, () => {
  console.log(signal);
  if (++count === 6) process.exit(0);
});
console.log('ready');
`);
  let output = "";
  let index = 0;
  const result = new Promise<number | null>((resolveExit, reject) => {
    launcher.once("error", reject);
    launcher.once("close", code => resolveExit(code));
    // Acknowledgement of each delivery avoids timing-dependent signal coalescing.
    launcher.stdout.on("data", data => {
      output += data.toString();
      const lines = output.trimEnd().split("\n");
      if (lines.length === index + 1 && index < signals.length) launcher.kill(signals[index++]);
    });
  });
  try {
    expect(await result).toBe(0);
    expect(output.trimEnd().split("\n")).toEqual(["ready", ...signals, "cleanup:true"]);
  } finally {
    if (launcher.exitCode === null) launcher.kill("SIGKILL");
  }
}, 15000);

test("launcher maps child signal exits to conventional codes and removes handlers", async () => {
  for (const [signal, code] of [["SIGINT", 130], ["SIGTERM", 143], ["SIGHUP", 129]] as const) {
    const launcher = launchFixture(`process.kill(process.pid, '${signal}');`);
    let output = "";
    launcher.stdout.on("data", data => { output += data.toString(); });
    const exitCode = await new Promise<number | null>((resolveExit, reject) => {
      launcher.once("error", reject);
      launcher.once("close", code => resolveExit(code));
    });
    expect(exitCode).toBe(code);
    expect(output.trim()).toBe("cleanup:true");
  }
});
