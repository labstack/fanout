import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { clipSegments, parseFreezeDetect } from "./clip-timeline.mjs";

const fixture = name => readFileSync(new URL(`./fixtures/freezedetect-${name}.stderr`, import.meta.url), "utf8");

test("freezedetect stderr parses paired intervals, including a freeze starting at zero", () => {
  const stderr = fixture("complete");
  for (const input of [stderr, stderr.replaceAll("\n", "\r\n")]) {
    assert.deepEqual(parseFreezeDetect(input, 12), [{ start: 0, end: 2.4 }, { start: 4.4, end: 9.8 }]);
  }
});

test("freezedetect closes an EOF freeze at the probed duration and accepts exponent timestamps", () => {
  assert.deepEqual(parseFreezeDetect(fixture("eof"), 10), [{ start: 0.12, end: 2.12 }, { start: 3.24, end: 10 }]);
  assert.deepEqual(parseFreezeDetect("[freezedetect] lavfi.freezedetect.freeze_start: 0", 10), [{ start: 0, end: 10 }]);
  assert.deepEqual(parseFreezeDetect("[freezedetect] lavfi.freezedetect.freeze_start: 10", 10), []);
});

test("non-freeze stderr and freeze_duration diagnostics do not create intervals", () => {
  assert.deepEqual(parseFreezeDetect("", 10), []);
  assert.deepEqual(parseFreezeDetect("frame=250 time=00:00:10.00\n[freezedetect] lavfi.freezedetect.freeze_duration: 2\n", 10), []);
});

test("invalid or unordered freezedetect timestamps fail rather than silently over-compress", () => {
  const log = events => events.map(([kind, at]) => `[freezedetect] lavfi.freezedetect.freeze_${kind}: ${at}\n`).join("");
  for (const events of [
    [["start", "NaN"]], [["start", "Infinity"]], [["start", ""]], [["start", -1]], [["start", 11]],
    [["end", 4]], [["start", 2], ["start", 3]], [["start", 2], ["end", 1]],
    [["start", 2], ["end", 2]], [["start", 2], ["end", 4], ["start", 3]],
  ]) assert.throws(() => parseFreezeDetect(log(events), 10));
  for (const duration of [0, -1, NaN, Infinity]) assert.throws(() => parseFreezeDetect("", duration));
  // Permit the same small container/frame rounding tolerance as timeline waits.
  assert.deepEqual(parseFreezeDetect(log([["start", 2], ["end", 10.1]]), 10), [{ start: 2, end: 10 }]);
});

test("freezes merge with waits before overlapping holds are subtracted, without mutating inputs", () => {
  const waits = Object.freeze([
    Object.freeze({ kind: "hold", start: 20, end: 28 }),
    Object.freeze({ kind: "model_thinking", start: 4, end: 8 }),
    Object.freeze({ kind: "hold", start: 12, end: 16 }),
    Object.freeze({ kind: "hold", start: 10, end: 14 }),
  ]);
  const freezes = Object.freeze([Object.freeze({ start: 7, end: 30 }), Object.freeze({ start: 0, end: 5 })]);
  assert.deepEqual(clipSegments(waits, 32, 2, freezes), [
    { start: 2, end: 4, seconds: 2 }, // Typing survives a detected freeze.
    { start: 4, end: 10, seconds: 0.6 }, // One merged wait/freeze, not two budgets.
    { start: 10, end: 16, seconds: 6 },
    { start: 16, end: 20, seconds: 0.6 },
    { start: 20, end: 28, seconds: 8 },
    { start: 28, end: 30, seconds: 0.6 },
    { start: 30, end: 32, seconds: 2 },
  ]);
});

test("a freeze spanning typing, waits, scroll and final dwell cannot shorten protected time", () => {
  assert.deepEqual(clipSegments([
    { kind: "model_thinking", start: 5, end: 10 },
    { kind: "hold", start: 18, end: 30 },
  ], 30, 2, [{ start: 0, end: 30 }]), [
    { start: 2, end: 5, seconds: 3 },
    { start: 5, end: 18, seconds: 0.6 },
    { start: 18, end: 30, seconds: 12 },
  ]);
  // Without a post-typing wait boundary, keep the opening conservatively.
  assert.deepEqual(clipSegments([{ kind: "hold", start: 10, end: 20 }], 20, 0, [{ start: 0, end: 20 }]), [
    { start: 0, end: 20, seconds: 20 },
  ]);
});

test("explicit typing takes priority over freezes and waits, and short remainders keep real speed", () => {
  assert.deepEqual(clipSegments([
    { kind: "typing", start: 2, end: 5 },
    { kind: "tool_call", start: 1, end: 10 },
    { kind: "hold", start: 10.2, end: 12 },
  ], 12, 0, [{ start: 8, end: 10.2 }]), [
    { start: 0, end: 1, seconds: 1 },
    { start: 1, end: 2, seconds: 0.6 },
    { start: 2, end: 5, seconds: 3 },
    { start: 5, end: 10.2, seconds: 0.6 },
    { start: 10.2, end: 12, seconds: 12 - 10.2 },
  ]);
  const segments = clipSegments([
    { kind: "typing", start: 0, end: 3 }, { kind: "hold", start: 3.4, end: 10 },
  ], 10, 0, [{ start: 0, end: 10 }]);
  assert.equal(segments[1].seconds, 3.4 - 3);
});

test("frozen intervals validate and clip at raw video and retained start boundaries", () => {
  for (const freezes of [null, [{ start: -1, end: 3 }], [{ start: 2, end: 1 }], [{ start: 0, end: Infinity }], [{ start: 5, end: 11 }]]) {
    assert.throws(() => clipSegments([], 10, 0, freezes));
  }
  assert.deepEqual(clipSegments([{ kind: "model_thinking", start: 1, end: 2 }], 10, 4, [
    { start: 0, end: 6 }, { start: 8, end: 10.1 },
  ]), [
    { start: 4, end: 6, seconds: 0.6 },
    { start: 6, end: 8, seconds: 2 },
    { start: 8, end: 10, seconds: 0.6 },
  ]);
});
