import { afterEach, expect, test } from "bun:test";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
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
  return root;
}

test("dotenv is optional and inherited values win, including empty values", () => {
  const root = fixture();
  expect(prepareEnvironment(root, { PATH: "/synthetic/path" })).toEqual({ PATH: "/synthetic/path" });
  writeFileSync(join(root, ".env"), "FANOUT_ADDR=:9000\nFANOUT_AI_MODEL=personal\nANTHROPIC_API_KEY=synthetic-only\nFANOUT_UNKNOWN=value\n");
  const env = prepareEnvironment(root, { FANOUT_ADDR: "", FANOUT_AI_MODEL: "injected", PATH: "/synthetic/path" });
  expect(env.FANOUT_ADDR).toBe("");
  expect(env.FANOUT_AI_MODEL).toBe("injected");
  expect(env.ANTHROPIC_API_KEY).toBe("synthetic-only");
  expect(env.FANOUT_UNKNOWN).toBe("value");
  expect(env.PATH).toBe("/synthetic/path");
});

test("dotenv parsing preserves literal shell text, CRLF, and multiline values", () => {
  const root = fixture();
  writeFileSync(join(root, ".env"), `FANOUT_DATA_DIR='  $HOME $(printf unsafe) "quoted"  '
FANOUT_AI_MODEL="line one
line two"
`.replace(/\n/g, "\r\n"));
  const env = prepareEnvironment(root, {});
  expect(env.FANOUT_DATA_DIR).toBe('  $HOME $(printf unsafe) "quoted"  ');
  expect(env.FANOUT_AI_MODEL).toBe("line one\nline two");
  const result = Bun.spawnSync(["node", "--input-type=module", "-e",
    "const {prepareEnvironment}=await import(process.argv[2]); process.stdout.write(JSON.stringify(prepareEnvironment(process.argv[1], {})));",
    root, pathToFileURL(join(import.meta.dir, "run-fanout.mjs")).href], { cwd: root });
  expect(result.exitCode).toBe(0);
  expect(JSON.parse(result.stdout.toString())).toEqual(env);
});

test("unreadable dotenv fails and launcher requires a command", async () => {
  const root = fixture();
  mkdirSync(join(root, ".env"));
  expect(() => prepareEnvironment(root, {})).toThrow("Cannot read dotenv");
  await expect(main([])).rejects.toThrow("Usage:");
  await expect(main(["--"])).rejects.toThrow("Usage:");
});

function launchFixture(childCode: string) {
  const root = fixture();
  mkdirSync(join(root, "scripts"));
  copyFileSync(join(import.meta.dir, "run-fanout.mjs"), join(root, "scripts/run-fanout.mjs"));
  const harness = `
import { main } from './scripts/run-fanout.mjs';
const signals = ['SIGINT', 'SIGTERM', 'SIGHUP'];
const before = signals.map(signal => process.listenerCount(signal));
const code = await main(['--', process.execPath, '-e', process.argv[2]]);
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
