import { spawn } from "node:child_process";
import { readFileSync, statSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { parseEnv } from "node:util";

/** @param {string} path @param {boolean} required */
function readDotenv(path, required) {
  let content;
  try {
    if (!statSync(path).isFile()) throw new Error("not a regular file");
    content = readFileSync(path, "utf8");
  } catch (error) {
    if (!required && error && typeof error === "object" && "code" in error && error.code === "ENOENT") return {};
    throw new Error(`Cannot read required or invalid dotenv file: ${path}`);
  }
  try {
    // Node's dotenv parser preserves literal dollars and never executes shell code.
    return parseEnv(content);
  } catch {
    throw new Error(`Invalid dotenv syntax: ${path}`);
  }
}

// The generated template is the schema-derived inventory, including secret
// classification. There is no independently maintained configuration allowlist.
/** @param {string} root */
function inventory(root) {
  let content;
  try {
    content = readFileSync(resolve(root, ".env.example"), "utf8");
  } catch {
    throw new Error("Missing generated environment inventory; run just docs-generate");
  }
  const names = new Set(Object.keys(parseEnv(content)));
  const secrets = new Set();
  for (const block of content.split(/\n\s*\n/)) {
    if (/^# [a-z0-9_.]+; [a-zA-Z0-9_.]+; secret$/.test(block.split(/\r?\n/, 1)[0])) {
      for (const name of Object.keys(parseEnv(block))) secrets.add(name);
    }
  }
  if (names.size === 0 || secrets.size === 0) throw new Error("Missing generated environment inventory; run just docs-generate");
  return { names, secrets };
}

/**
 * @param {string} root
 * @param {"local" | "production"} environment
 * @param {Record<string, string | undefined>} inherited
 * @returns {Record<string, string>}
 */
export function prepareEnvironment(root, environment, inherited) {
  if (environment !== "local" && environment !== "production") throw new Error("Environment must be local or production");
  const { names, secrets } = inventory(root);
  const files = [".env", ...(environment === "production" ? [".env.production"] : [".env.local"])];
  /** @type {Record<string, string>} */
  const values = {};
  for (const file of files) {
    const layer = readDotenv(resolve(root, file), file !== ".env.local");
    for (const [name, value] of Object.entries(layer)) {
      if (value === undefined) throw new Error(`Invalid dotenv syntax: ${file}`);
      if (!names.has(name)) throw new Error(`Unknown dotenv setting ${name} in ${file}`);
      if (secrets.has(name)) throw new Error(`Secret setting ${name} belongs in runtime injection, not ${file}`);
      if (value.includes("op://")) throw new Error(`Unresolved secret reference for ${name} in ${file}; use runtime injection`);
      if (value !== "") values[name] = value;
    }
  }
  // Explicitly injected values win. An empty recognized value retains Fanout's
  // existing "no override" semantics instead of erasing a file setting.
  for (const [name, value] of Object.entries(inherited)) {
    if (value !== undefined && (value !== "" || !names.has(name))) values[name] = value;
  }
  return values;
}

/** @param {string[]} args @returns {Promise<number>} */
export async function main(args) {
  if (args.length < 4 || args[0] !== "--environment" || args[2] !== "--") {
    throw new Error("Usage: node scripts/run-fanout.mjs --environment local|production -- command [args]");
  }
  const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  const environment = /** @type {"local" | "production"} */ (args[1]);
  const env = prepareEnvironment(root, environment, process.env);
  const child = spawn(args[3], args.slice(4), { cwd: root, env, stdio: "inherit" });
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
    // Launch errors can contain command arguments. Only preparation errors
    // below carry safe setting/file names; never print resolved environment values.
    const message = error instanceof Error ? error.message : "Environment launch failed";
    console.error(message.startsWith("Unknown dotenv") || message.startsWith("Secret setting") ||
      message.startsWith("Unresolved secret") || message.startsWith("Cannot read") ||
      message.startsWith("Invalid dotenv") || message.startsWith("Usage:") ||
      message.startsWith("Environment must") || message.startsWith("Missing generated") ? message : "Cannot launch command; ensure it is executable (for Fanout, run just build)");
    process.exitCode = 1;
  }
}
