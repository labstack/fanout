import { test } from "node:test";
import assert from "node:assert/strict";
import { recordingTimeline } from "../../ui/host/e2e/landing-recording-support.ts";
import { clipSegments, clipFilter } from "./clip-timeline.mjs";

const stream = events => events.map(event => `data: ${JSON.stringify(event)}\n\n`).join("");
const event = (type, at, data = {}) => ({ type, timestamp: 1_000_000 + at * 1000, ...data });

test("recording times survive server clock skew and preserve visible reasoning and answer text", () => {
  const recorded = recordingTimeline(stream([
    event("RUN_STARTED", 0), event("REASONING_MESSAGE_START", 2), event("REASONING_MESSAGE_CONTENT", 3),
    event("REASONING_MESSAGE_END", 4), event("TOOL_CALL_START", 6, { toolCallId: "q", toolCallName: "query_telemetry" }),
    event("TOOL_CALL_END", 6.1, { toolCallId: "q" }), // Arguments ended; the tool is still running.
    event("TOOL_CALL_RESULT", 9, { toolCallId: "q", content: "{}" }),
    event("TEXT_MESSAGE_START", 12), event("TEXT_MESSAGE_CONTENT", 13), event("RUN_FINISHED", 14),
  ]), 10);
  assert.deepEqual(recorded.waits, [
    { kind: "model_thinking", start: 10, end: 12 },
    { kind: "model_thinking", start: 14, end: 16 },
    { kind: "tool_call", start: 16, end: 19, tool: "query_telemetry" },
    { kind: "model_thinking", start: 19, end: 22 },
  ]);
});

test("concurrent tools remain distinct and only a successful create receipt identifies the new dashboard", () => {
  const created = { dashboard: { id: "new-dashboard" }, receipt: { version: 1 } };
  for (const result of [created, { ...created, error: "failed" }, { ...created, isError: true }, { dashboard: created.dashboard }]) {
    const timeline = recordingTimeline(stream([
      event("RUN_STARTED", 0),
      event("TOOL_CALL_START", 1, { toolCallId: "a", toolCallName: "create_dashboard" }),
      event("TOOL_CALL_START", 2, { toolCallId: "b", toolCallName: "search_logs" }),
      event("TOOL_CALL_RESULT", 3, { toolCallId: "a", content: JSON.stringify(result) }),
      event("TOOL_CALL_RESULT", 4, { toolCallId: "b", content: "{}" }),
      event("TEXT_MESSAGE_START", 5), event("RUN_FINISHED", 6),
    ]).replaceAll("\n", "\r\n"), 0);
    assert.equal(timeline.dashboardId, result === created ? "new-dashboard" : undefined);
    assert.deepEqual(timeline.waits.filter(wait => wait.kind === "model_thinking"), [
      { kind: "model_thinking", start: 0, end: 1 }, { kind: "model_thinking", start: 4, end: 5 },
    ]);
    assert.equal(timeline.waits.filter(wait => wait.kind === "tool_call").length, 2);
  }
});

test("failed, truncated, untimed or unresolved runs cannot become successful recordings", () => {
  const start = event("RUN_STARTED", 0), end = event("RUN_FINISHED", 10);
  for (const events of [[], [start], [start, event("RUN_ERROR", 1), end],
    [start, { type: "TEXT_MESSAGE_START" }, end],
    [start, event("TOOL_CALL_START", 1, { toolCallId: "a", toolCallName: "query_telemetry" }), end],
    [start, event("TOOL_CALL_RESULT", 1, { toolCallId: "a" }), end]]) {
    assert.throws(() => recordingTimeline(stream(events), 0));
  }
});

test("encoding merges overlapping waits, keeps short waits and preserves typing and dwell", () => {
  const segments = clipSegments([{ start: 5, end: 12 }, { start: 4, end: 8 }, { start: 15, end: 15.2 }], 20);
  assert.deepEqual(segments, [
    { start: 0, end: 4, seconds: 4 }, { start: 4, end: 12, seconds: 0.6 },
    { start: 12, end: 15, seconds: 3 }, { start: 15, end: 15.2, seconds: 15.2 - 15 },
    { start: 15.2, end: 20, seconds: 20 - 15.2 },
  ]);
  const filter = clipFilter(segments);
  assert.match(filter, /trim=start=4:end=12,setpts=\(PTS-STARTPTS\)\*0.075\[v1\]/);
  assert.match(filter, /concat=n=5:v=1:a=0,scale=1280:800,fps=30,format=yuv420p\[out\]/);
  assert.deepEqual(clipSegments([], 3), [{ start: 0, end: 3, seconds: 3 }]);
});

test("invalid trim times fail before invoking ffmpeg", () => {
  for (const waits of [[{ start: -1, end: 2 }], [{ start: 3, end: 2 }], [{ start: 0, end: Infinity }], [{ start: 1, end: 11 }], null]) {
    assert.throws(() => clipSegments(waits, 10));
  }
  assert.throws(() => clipSegments([], 0));
});

test("encoding preserves overlapping holds at real speed inside merged waits", () => {
  const segments = clipSegments([
    { kind: "tool_call", start: 2, end: 10 },
    { kind: "model_thinking", start: 8, end: 18 },
    { kind: "hold", start: 6, end: 12 },
    { kind: "hold", start: 4, end: 9 },
  ], 20);
  assert.deepEqual(segments, [
    { start: 0, end: 2, seconds: 2 },
    { start: 2, end: 4, seconds: 0.6 },
    { start: 4, end: 12, seconds: 8 },
    { start: 12, end: 18, seconds: 0.6 },
    { start: 18, end: 20, seconds: 2 },
  ]);
  assert.match(clipFilter(segments), /trim=start=4:end=12,setpts=\(PTS-STARTPTS\)\*1\[v2\]/);
});

test("holds can cover entire waits and are clipped at the retained video boundaries", () => {
  assert.deepEqual(clipSegments([
    { kind: "hold", start: 1, end: 10.1 },
    { kind: "tool_call", start: 2, end: 8 },
  ], 10, 4), [{ start: 4, end: 10, seconds: 6 }]);
  assert.deepEqual(clipSegments([{ kind: "hold", start: 2, end: 6 }], 10), [
    { start: 0, end: 10, seconds: 10 },
  ]);
  assert.throws(() => clipSegments([{ kind: "hold", start: 2, end: 1 }], 10));
});

test("encoding cuts frames before the clip start", () => {
  const segments = clipSegments([{ start: 1, end: 3 }, { start: 5, end: 8 }], 12, 4);
  assert.deepEqual(segments, [
    { start: 4, end: 5, seconds: 1 },
    { start: 5, end: 8, seconds: 0.6 },
    { start: 8, end: 12, seconds: 4 },
  ]);
  assert.throws(() => clipSegments([], 12, 12), /Clip start must be within the raw video/);
});
