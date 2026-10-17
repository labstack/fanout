import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { clipObject, uploadClips } from "./upload-clips.mjs";

function setup(t) {
  const directory = mkdtempSync(join(tmpdir(), "fanout-upload-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const input = join(directory, "encoded clips");
  const manifestPath = join(directory, "site/src/data/landing-media.json");
  mkdirSync(input);
  mkdirSync(dirname(manifestPath), { recursive: true });
  return { directory, input, manifestPath };
}

test("object keys and public URLs use the first 12 hex digits of SHA-256", () => {
  for (const format of ["webm", "mp4"]) {
    assert.deepEqual(clipObject("chat-light", format, Buffer.from("abc")), {
      key: `landing/chat-light-ba7816bf8f01.${format}`,
      url: `https://assets.fanout.run/landing/chat-light-ba7816bf8f01.${format}`,
    });
    assert.notEqual(clipObject("chat-light", format, Buffer.from("abcd")).key,
      clipObject("chat-light", format, Buffer.from("abc")).key);
  }
});

test("shell wrapper uploads all eight formats with exact Wrangler flags and skips unchanged keys", t => {
  const { directory, input, manifestPath } = setup(t);
  const scripts = join(directory, "site/scripts");
  const bin = join(directory, "bin");
  const log = join(directory, "uploads.jsonl");
  mkdirSync(scripts);
  mkdirSync(bin);
  for (const script of ["upload-clips.sh", "upload-clips.mjs"]) {
    copyFileSync(new URL(`./${script}`, import.meta.url), join(scripts, script));
  }
  writeFileSync(join(bin, "wrangler"), `#!/usr/bin/env node
require("node:fs").appendFileSync(process.env.CLIP_UPLOAD_LOG, JSON.stringify(process.argv.slice(2)) + "\\n");
`, { mode: 0o755 });
  const names = ["chat-dark", "chat-light", "dashboard-dark", "dashboard-light"];
  for (const name of names) {
    for (const format of ["mp4", "webm"]) writeFileSync(join(input, `${name}.${format}`), "abc");
  }
  writeFileSync(join(input, "chat-light.png"), "poster");
  writeFileSync(join(input, "unrelated.mp4"), "unrelated");
  const run = args => {
    const result = spawnSync("bash", [join(scripts, "upload-clips.sh"), ...args], {
      cwd: tmpdir(), encoding: "utf8",
      env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, CLIP_UPLOAD_LOG: log },
    });
    assert.equal(result.status, 0, result.stderr);
  };
  run([input]);
  const uploads = readFileSync(log, "utf8").trim().split("\n").map(line => JSON.parse(line));
  assert.equal(uploads.length, 8);
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  for (const [index, [name, format]] of names.flatMap(name => ["mp4", "webm"].map(format => [name, format])).entries()) {
    const { key, url } = clipObject(name, format, Buffer.from("abc"));
    assert.deepEqual(uploads[index], ["r2", "object", "put", `fanout-assets/${key}`, "--file", join(input, `${name}.${format}`),
      "--content-type", `video/${format}`, "--cache-control", "public, max-age=31536000, immutable", "--remote"]);
    assert.equal(manifest[name][format], url);
  }
  assert.deepEqual(Object.keys(manifest), names);
  for (const clip of Object.values(manifest)) assert.deepEqual(Object.keys(clip), ["mp4", "webm"]);
  assert.ok(readFileSync(manifestPath, "utf8").endsWith("\n"));
  run([input]);
  assert.equal(readFileSync(log, "utf8").trim().split("\n").length, 8);
  // The default input is relative to the script, regardless of the caller's cwd.
  mkdirSync(join(directory, "site/src/assets/landing"), { recursive: true });
  writeFileSync(join(directory, "site/src/assets/landing/chat-dark.mp4"), "abc");
  run([]);
  assert.equal(readFileSync(log, "utf8").trim().split("\n").length, 8);
});

test("partial retakes upload changed content and preserve missing clips", t => {
  const { input, manifestPath } = setup(t);
  const original = JSON.parse(readFileSync(new URL("./fixtures/landing-media.json", import.meta.url), "utf8"));
  writeFileSync(manifestPath, JSON.stringify(original));
  writeFileSync(join(input, "chat-light.mp4"), "retake");
  const uploads = [];
  const actual = uploadClips({ input, manifestPath, upload: args => uploads.push(args) });
  assert.equal(uploads.length, 1);
  const expected = structuredClone(original);
  expected["chat-light"].mp4 = clipObject("chat-light", "mp4", Buffer.from("retake")).url;
  assert.deepEqual(actual, expected);
  assert.deepEqual(JSON.parse(readFileSync(manifestPath, "utf8")), expected);
});

test("upload failures leave the published manifest untouched", t => {
  const { input, manifestPath } = setup(t);
  writeFileSync(manifestPath, "{}\n");
  writeFileSync(join(input, "chat-dark.mp4"), "abc");
  writeFileSync(join(input, "chat-light.webm"), "retake");
  let calls = 0;
  assert.throws(() => uploadClips({ input, manifestPath, upload: () => {
    if (++calls === 2) throw new Error("upload failed");
  } }), /upload failed/);
  assert.equal(calls, 2);
  assert.equal(readFileSync(manifestPath, "utf8"), "{}\n");
});
