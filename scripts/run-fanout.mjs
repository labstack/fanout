import { spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { parseEnv } from "node:util";

/** @param {string} root @param {Record<string, string | undefined>} inherited */
export function prepareEnvironment(root, inherited) {
  let content = "";
  try {
    content = readFileSync(resolve(root, ".env"), "utf8");
  } catch (error) {
    if (!error || typeof error !== "object" || !("code" in error) || error.code !== "ENOENT") {
      throw new Error("Cannot read dotenv file: .env");
    }
  }
  // Node's parser preserves literal dollars and never executes shell code.
  const values = parseEnv(content);
  for (const [name, value] of Object.entries(inherited)) {
    if (value !== undefined) values[name] = value;
  }
  return values;
}

/** @param {string[]} args @returns {Promise<number>} */
export async function main(args) {
  if (args.length < 2 || args[0] !== "--") {
    throw new Error("Usage: node scripts/run-fanout.mjs -- command [args]");
  }
  const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  const env = prepareEnvironment(root, process.env);
  const child = spawn(args[1], args.slice(2), { cwd: root, env, stdio: "inherit" });
  const handlers = new Map(["SIGINT", "SIGTERM", "SIGHUP"].map(signal => [signal, () => child.kill(signal)]));
  for (const [signal, handler] of handlers) process.on(signal, handler);
  try {
    return await new Promise((resolveExit, reject) => {
      child.once("error", reject);
      child.once("exit", (code, signal) => resolveExit(code ?? ({ SIGINT: 130, SIGTERM: 143, SIGHUP: 129 }[signal] ?? 1)));
    });
  } finally {
    for (const [signal, handler] of handlers) process.off(signal, handler);
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    process.exitCode = await main(process.argv.slice(2));
  } catch (error) {
    const message = error instanceof Error ? error.message : "Environment launch failed";
    console.error(message.startsWith("Cannot read dotenv") || message.startsWith("Usage:")
      ? message : "Cannot launch command; ensure it is executable (for Fanout, run just build)");
    process.exitCode = 1;
  }
}
