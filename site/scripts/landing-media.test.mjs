import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { hasProductVideo } from "../src/components/landing/landing-media.ts";

const fixture = JSON.parse(readFileSync(new URL("./fixtures/landing-media.json", import.meta.url), "utf8"));

test("playback requires both formats in both themes, independently for each view", () => {
  for (const view of ["chat", "dashboard"]) {
    const sources = ["light", "dark"].flatMap(theme => ["webm", "mp4"].map(format => [`${view}-${theme}`, format]));
    for (let mask = 0; mask < 16; mask++) {
      const manifest = structuredClone(fixture);
      sources.forEach(([name, format], index) => { if (!(mask & (1 << index))) delete manifest[name][format]; });
      assert.equal(hasProductVideo(manifest, view), mask === 15, `${view}: ${mask}`);
      assert.equal(hasProductVideo(manifest, view === "chat" ? "dashboard" : "chat"), true);
    }
  }
});

test("missing entries or empty URLs keep the poster fallback", () => {
  assert.equal(hasProductVideo({}, "chat"), false);
  for (const theme of ["light", "dark"]) {
    const manifest = structuredClone(fixture);
    delete manifest[`chat-${theme}`];
    assert.equal(hasProductVideo(manifest, "chat"), false);
    for (const invalid of ["", null, 42]) {
      const manifest = structuredClone(fixture);
      manifest[`chat-${theme}`].mp4 = invalid;
      assert.equal(hasProductVideo(manifest, "chat"), false);
    }
  }
});
