import { test } from "node:test";
import assert from "node:assert/strict";
import { runInNewContext } from "node:vm";
import { revealRecording, waitForConversation } from "../../ui/host/e2e/support/landing-recording-support.ts";

// Execute the actual page.evaluate callbacks with a virtual layout and clock.
// No browser is launched; iframe growth is represented by moving the answer.
function recordingPage({ view = "chat", answerReady = true } = {}) {
  let now = 0, nextId = 0, scrollTop = 0, answerBottom = 1_300;
  const pending = new Map(), observers = new Set(), positions = [];
  const schedule = (callback, milliseconds) => {
    const id = ++nextId;
    pending.set(id, { at: now + milliseconds, callback });
    return id;
  };
  class Observer {
    constructor(callback) { this.callback = callback; this.targets = new Set(); observers.add(this); }
    observe(target) { this.targets.add(target); }
    disconnect() { observers.delete(this); }
  }
  const answer = { getBoundingClientRect: () => ({ bottom: 100 + answerBottom - scrollTop }) };
  const markdown = { textContent: "The final answer." };
  const content = {};
  const scroller = {
    clientHeight: 500, clientTop: 0, firstElementChild: content,
    get scrollHeight() { return view === "chat" ? answerBottom + 24 : 1_400; },
    get scrollTop() { return scrollTop; },
    set scrollTop(value) {
      scrollTop = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight));
      positions.push({ at: now, top: scrollTop });
    },
    getBoundingClientRect: () => ({ top: 100 }),
    querySelector: selector => selector === "[data-answer-position]" ? answer : answerReady ? markdown : null,
    querySelectorAll: () => [answer],
    dispatchEvent() {},
  };
  const environment = {
    document: { querySelector: () => scroller, scrollingElement: scroller, fonts: { ready: Promise.resolve() } },
    performance: { now: () => now },
    requestAnimationFrame: callback => schedule(() => callback(now), 16),
    cancelAnimationFrame: id => pending.delete(id),
    setTimeout: schedule, clearTimeout: id => pending.delete(id),
    ResizeObserver: Observer, MutationObserver: Observer, WheelEvent: class {},
  };
  const page = {
    async evaluate(callback, argument) {
      let done = false, error;
      const result = runInNewContext(`(${callback.toString()})(argument)`, { ...environment, argument });
      result.then(() => { done = true; }, reason => { done = true; error = reason; });
      for (let steps = 0; !done && steps < 10_000; steps++) {
        // Drain async continuations before advancing the simulated animation clock.
        await new Promise(resolve => setImmediate(resolve));
        if (done) break;
        const next = [...pending].sort((a, b) => a[1].at - b[1].at)[0];
        assert.ok(next, "evaluate must resolve or schedule more work");
        pending.delete(next[0]);
        now = next[1].at;
        next[1].callback();
      }
      assert.ok(done, "evaluate must finish within the virtual clock budget");
      if (error) throw error;
      return result;
    },
  };
  return {
    page, positions, observers, pending,
    get now() { return now; },
    get expectedEnd() { return answerBottom - scroller.clientHeight; },
    get top() { return scrollTop; },
    resizeAt(at, bottom) {
      schedule(() => {
        answerBottom = bottom;
        for (const observer of observers) observer.callback();
      }, at);
    },
    renderAnswerAt(at) { schedule(() => { answerReady = true; }, at); },
  };
}

test("chat retargets late evidence growth during the scroll and holds the answer end for 4 seconds", async () => {
  const recording = recordingPage();
  recording.resizeAt(3_000, 2_400);
  recording.resizeAt(9_500, 3_100); // Growth during the final dwell must also stay aligned.
  await revealRecording(recording.page, "chat");
  assert.equal(recording.top, recording.expectedEnd);
  assert.ok(recording.positions.some(position => position.at > 8_100 && position.at < 9_500 && position.top === 1_900));
  assert.ok(recording.positions.filter(position => position.at >= 9_520).every(position => position.top === 2_600));
  // Initial distance sets an 8-second scroll; growth must not restart its duration.
  assert.ok(recording.now >= 12_000 && recording.now < 12_150);
});

test("dashboard keeps the down/bottom/up timings and dwells at the top for only 3 seconds", async () => {
  const recording = recordingPage({ view: "dashboard" });
  await revealRecording(recording.page, "dashboard");
  assert.equal(recording.top, 0);
  assert.equal(Math.max(...recording.positions.map(position => position.top)), 900);
  assert.ok(recording.positions.some(position => position.top > 0 && position.top < 900 && position.at > 7_500));
  assert.ok(recording.now >= 12_000 && recording.now < 12_150);
  assert.equal(recording.now - recording.positions.at(-1).at, 3_000);
});

test("conversation readiness waits for rendered text and 1.5 seconds after the last resize", async () => {
  const recording = recordingPage({ answerReady: false });
  recording.renderAnswerAt(1_500);
  recording.resizeAt(1_000, 2_000);
  recording.resizeAt(2_000, 2_400);
  await waitForConversation(recording.page);
  assert.ok(recording.now >= 3_500 && recording.now < 3_550);
  assert.equal(recording.observers.size, 0);
  assert.equal(recording.pending.size, 0);
});

test("conversation readiness fails instead of recording an answer that never renders", async () => {
  const recording = recordingPage({ answerReady: false });
  await assert.rejects(waitForConversation(recording.page), /Conversation did not stop resizing/);
  assert.equal(recording.now, 30_000);
  assert.equal(recording.observers.size, 0);
  assert.equal(recording.pending.size, 0);
});
