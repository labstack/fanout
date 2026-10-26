import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, realpathSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const defaultInput = fileURLToPath(new URL("../src/assets/landing/", import.meta.url));
const defaultManifest = fileURLToPath(new URL("../src/data/landing-media.json", import.meta.url));
const names = ["chat-dark", "chat-light", "dashboard-dark", "dashboard-light"];
const formats = ["mp4", "webm"];

export function clipObject(name, format, contents) {
  const hash = createHash("sha256").update(contents).digest("hex").slice(0, 12);
  const key = `landing/${name}-${hash}.${format}`;
  return { key, url: `https://assets.fanout.run/${key}` };
}

function put(args) {
  const result = spawnSync("wrangler", args, { stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`wrangler exited with status ${result.status}`);
}

export function uploadClips({ input = defaultInput, manifestPath = defaultManifest, upload = put } = {}) {
  input = resolve(input);
  const files = new Set(readdirSync(input));
  const manifest = existsSync(manifestPath) ? JSON.parse(readFileSync(manifestPath, "utf8")) : {};
  for (const name of names) {
    for (const format of formats) {
      const filename = `${name}.${format}`;
      if (!files.has(filename)) continue;
      const file = join(input, filename);
      const { key, url } = clipObject(name, format, readFileSync(file));
      if (manifest[name]?.[format] === url) continue;
      upload(["r2", "object", "put", `fanout/${key}`, "--file", file,
        "--content-type", `video/${format}`, "--cache-control", "public, max-age=31536000, immutable", "--remote"]);
      manifest[name] = { ...manifest[name], [format]: url };
    }
  }
  // Retakes may contain only one clip; preserve all other published URLs.
  const sorted = Object.fromEntries(Object.keys(manifest).sort().map(name => [name,
    Object.fromEntries(Object.entries(manifest[name]).sort(([a], [b]) => a.localeCompare(b))),
  ]));
  mkdirSync(dirname(manifestPath), { recursive: true });
  // Publish only after every upload succeeds, and never expose a partial JSON file.
  const temporary = `${manifestPath}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(sorted, null, 2)}\n`);
  renameSync(temporary, manifestPath);
  return sorted;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === realpathSync(process.argv[1])) {
  if (process.argv.length > 3) throw new Error("Usage: bash site/scripts/upload-clips.sh [encoded-directory]");
  uploadClips({ input: process.argv[2] });
}
