import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { clock, replayFrame, mountReplay } from "../src/components/landing/throughput-replay.ts";
import { benchmark, formatRate } from "../src/data/benchmark.ts";

const source = readFileSync(new URL("../src/components/landing/ThroughputReplay.astro", import.meta.url), "utf8");
const client = readFileSync(new URL("../src/components/landing/ThroughputReplayClient.astro", import.meta.url), "utf8");
const { sustained } = benchmark;
const averages = {
  "[data-counter]": formatRate(sustained.accepted_per_second),
  "[data-value=spans]": formatRate(sustained.spans_per_second),
  "[data-value=logs]": formatRate(sustained.log_records_per_second),
  "[data-value=metrics]": formatRate(sustained.metric_points_per_second),
};
function assertAverages(s) {
  for (const [selector, value] of Object.entries(averages)) assert.equal(s.element(selector).textContent, value);
}
function assertRate(s, total) {
  assert.equal(s.element("[data-rate]").hidden, false);
  assert.equal(s.element("[data-rate]").textContent, `· ${formatRate(total)} items/s, 5 s rate`);
  assertAverages(s);
}
const samples = [
  { at_seconds: 1, interval_seconds: 0.9, spans: 10, logs: 20, metrics: 30 },
  { at_seconds: 2, interval_seconds: 1, spans: 20, logs: 30, metrics: 40 },
  { at_seconds: 4, interval_seconds: 2, spans: 40, logs: 50, metrics: 60 },
];

test("rolling stacks preserve measured timestamps, signal values and unequal intervals", () => {
  const frame = replayFrame(samples, 5, 4.5);
  assert.deepEqual(frame.data, [[1, 2, 4, 4.5], [10, 20, 40, 40], [30, 50, 90, 90], [60, 90, 150, 150]]);
  assert.equal(frame.current.interval_seconds, 2);
  assert.equal(frame.max - frame.min, 120);
  assert.equal(frame.current.at_seconds, 4);
  assert.equal(clock(137), "02:17");
  assert.equal(clock(303.9), "05:03");
});

test("wraps retain history and never blank or invent values before the next sample", () => {
  assert.equal(replayFrame(samples, 5, 5).current.spans, 40);
  const next = replayFrame(samples, 5, 6);
  assert.deepEqual(next.points.map(p => p.time), [1, 2, 4, 6]);
  assert.equal(next.current.spans, 10);
  const later = replayFrame(samples, 5, 1006);
  assert.equal(later.max - later.min, 120);
  assert.ok(later.points[0].time <= later.min);
  assert.ok(later.points[1].time >= later.min);
  assert.ok(later.points.length < 80, "memory stays bounded across hundreds of loops");
  assert.equal(later.current.at_seconds, 1);
});

function events(initial = {}) {
  const listeners = new Map();
  return { ...initial, listeners,
    addEventListener(type, fn) { if (!listeners.has(type)) listeners.set(type, new Set()); listeners.get(type).add(fn); },
    removeEventListener(type, fn) { listeners.get(type)?.delete(fn); },
    dispatch(type, event = {}) { for (const fn of listeners.get(type) ?? []) fn(event); },
  };
}
const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
function setup(t, { reduced = false, deferred = false, series = samples, duration = 5 } = {}) {
  const timers = new Map(); let now = 0, next = 0, loads = 0, themeColor = "dark";
  const observers = {};
  const elements = new Map();
  const element = key => {
    if (!elements.has(key)) elements.set(key, events({ textContent: averages[key] ?? (key === "[data-rate]" ? "" : "average"),
      hidden: true, attrs: {}, setAttribute(key, value) { this.attrs[key] = value; },
      getBoundingClientRect: () => ({ width: 600, height: 240 }) }));
    return elements.get(key);
  };
  const root = { dataset: { series: JSON.stringify(series), seconds: String(duration), ceiling: "300", step: "100" }, querySelector: element };
  const motion = events({ matches: reduced });
  const doc = events({ hidden: false, documentElement: {} });
  const win = events();
  const plots = [];
  class Plot {
    static pxRatio = 2;
    constructor(opts, data) {
      this.opts = opts; this.data = data; this.root = element("plot-root"); this.width = opts.width; this.height = opts.height;
      this.cursor = { idx: null, left: -1 }; this.redraws = 0; this.updates = 0;
      plots.push(this);
    }
    batch(fn) { fn(); }
    setData(data) { this.data = data; this.updates++; }
    setScale(_, limits) { this.limits = limits; }
    setSize(size) { Object.assign(this, size); }
    redraw() { this.redraws++; }
    destroy() { this.destroyed = true; }
  }
  let resolveLoad;
  const promise = new Promise(resolve => { resolveLoad = resolve; });
  const globals = {
    document: doc, window: win, matchMedia: () => motion,
    getComputedStyle: () => ({ getPropertyValue: key => key.includes("font") ? "monospace" : `${themeColor}:${key}` }),
    IntersectionObserver: class { constructor(callback) { observers.intersection = this; this.callback = callback; } observe() {} disconnect() { this.disconnected = true; } },
    ResizeObserver: class { constructor(callback) { observers.size = this; this.callback = callback; } observe() {} disconnect() { this.disconnected = true; } },
    MutationObserver: class { constructor(callback) { observers.theme = this; this.callback = callback; } observe() {} disconnect() { this.disconnected = true; } },
    setTimeout: (callback, delay) => { timers.set(++next, { callback, at: now + delay }); return next; },
    clearTimeout: id => timers.delete(id),
  };
  const restore = [];
  for (const [key, value] of Object.entries(globals)) {
    const before = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
    restore.push(() => { if (before) Object.defineProperty(globalThis, key, before); else delete globalThis[key]; });
  }
  t.mock.method(performance, "now", () => now);
  const cleanup = mountReplay(root, () => { loads++; return deferred ? promise : Promise.resolve(Plot); });
  t.after(() => { cleanup(); restore.reverse().forEach(fn => fn()); });
  const advance = ms => {
    const target = now + ms;
    while (true) {
      const entry = [...timers.entries()].find(([, timer]) => timer.at <= target);
      if (!entry) break;
      const [id, timer] = entry; timers.delete(id); now = timer.at; timer.callback();
    }
    now = target;
  };
  return { root, motion, doc, win, observers, plots, timers, element, advance, cleanup,
    get loads() { return loads; }, resolveLoad: () => resolveLoad(Plot),
    visible: async value => { observers.intersection.callback([{ isIntersecting: value }]); await flush(); },
    theme: value => { themeColor = value; observers.theme.callback(); },
  };
}

test("initial long-series frame fills the window with measured samples at the real replay position", async t => {
  const series = Array.from({ length: 150 }, (_, i) => ({ ...samples[i % samples.length], at_seconds: 2 + i * 2 }));
  const s = setup(t, { series, duration: 300 });
  await s.visible(true);
  const plot = s.plots[0];
  assert.deepEqual(plot.opts.scales.x.range(), [2, 122]);
  assert.deepEqual(plot.data[0], series.slice(0, 61).map(point => point.at_seconds));
  assert.deepEqual(plot.data[1], series.slice(0, 61).map(point => point.spans));
  assertRate(s, 60);
  assert.equal(s.element("[data-elapsed]").textContent, "02:02 / 05:00");
  s.advance(1000);
  assert.deepEqual(plot.limits, { min: 3, max: 123 });
  assertRate(s, 60); // Hold the last measured rate between samples.
  s.advance(1000);
  assertRate(s, 90);
});

test("initial short-series frame shows the whole series and streams the next loop from the right", async t => {
  const s = setup(t);
  await s.visible(true);
  const plot = s.plots[0];
  assert.deepEqual(plot.opts.scales.x.range(), [-116, 4]);
  assert.deepEqual(plot.data, [[1, 2, 4], [10, 20, 40], [30, 50, 90], [60, 90, 150]]);
  assertRate(s, 150);
  assert.equal(s.element("[data-elapsed]").textContent, "00:04 / 00:05");
  s.advance(1000);
  assert.deepEqual(plot.data[0], [1, 2, 4, 5]);
  assertRate(s, 150); // Hold the last measured value at the loop boundary.
  s.advance(1000);
  assert.deepEqual(plot.data[0], [1, 2, 4, 6]);
  assertRate(s, 60);
});

test("one measured second per wall second, with fixed averages and live status and hover rates", async t => {
  const s = setup(t);
  assert.equal(s.loads, 0, "uPlot loads only when the hero becomes visible");
  await s.visible(true);
  const plot = s.plots[0];
  assertRate(s, 150);
  assert.equal(plot.root.attrs["aria-hidden"], "true");
  assert.deepEqual(plot.opts.bands.map(band => band.series), [[2, 1], [3, 2]]);
  s.advance(1000);
  assertRate(s, 150);
  assert.equal(s.element("[data-state]").textContent, "Replay 2");
  s.advance(1000);
  assertRate(s, 60);
  assert.equal(s.element("[data-elapsed]").textContent, "00:01 / 00:05");
  assert.equal(plot.limits.max - plot.limits.min, 120);
  plot.cursor = { idx: 1, left: 100 }; plot.opts.hooks.setCursor[0](plot);
  assert.equal(s.element("[data-tooltip]").hidden, false);
  assert.equal(s.element("[data-tooltip-time]").textContent, "00:02 · items/s, 5 s rate");
  assert.equal(s.element("[data-hover=spans]").textContent, "20");
  assert.equal(s.element("[data-hover=logs]").textContent, "30", "hover reports raw signal values");
  assert.equal(s.element("[data-hover=metrics]").textContent, "40");
  assertRate(s, 60); // Hover changes neither the averages nor the live total.
  plot.cursor.left = -1; plot.opts.hooks.setCursor[0](plot);
  assert.equal(s.element("[data-tooltip]").hidden, true);
  s.advance(1000);
  assertRate(s, 90);
  s.advance(2000);
  assertRate(s, 150);
});

test("hidden, offscreen and user pauses cancel timers and exclude paused wall time", async t => {
  const s = setup(t); await s.visible(true);
  s.advance(250); s.doc.hidden = true; s.doc.dispatch("visibilitychange");
  assert.equal(s.timers.size, 0);
  s.advance(300000); s.doc.hidden = false; s.doc.dispatch("visibilitychange");
  assert.equal(s.element("[data-elapsed]").textContent, "00:04 / 00:05");
  s.advance(1000);
  assert.equal(s.element("[data-elapsed]").textContent, "00:00 / 00:05");
  await s.visible(false); assert.equal(s.timers.size, 0);
  s.advance(300000); await s.visible(true);
  assert.equal(s.element("[data-elapsed]").textContent, "00:00 / 00:05");
  s.element("[data-replay]").dispatch("click");
  assert.equal(s.element("[data-replay]").textContent, "Play"); assert.equal(s.timers.size, 0);
  assertRate(s, 150);
  await s.visible(false); await s.visible(true); s.doc.dispatch("visibilitychange");
  assert.equal(s.timers.size, 0, "manual pause survives observer events");
  s.element("[data-replay]").dispatch("click"); assert.equal(s.timers.size, 1);
});

test("reduced motion keeps SVG and averages, even when toggled during a deferred import", async t => {
  const s = setup(t, { reduced: true, deferred: true });
  await s.visible(true); assert.equal(s.loads, 0); assert.equal(s.timers.size, 0);
  assert.equal(s.root.dataset.mounted, undefined);
  assertAverages(s);
  assert.equal(s.element("[data-rate]").hidden, true);
  assert.equal(s.element("[data-rate]").textContent, "");
  s.motion.matches = false; s.motion.dispatch("change"); assert.equal(s.loads, 1);
  s.motion.matches = true; s.motion.dispatch("change"); s.resolveLoad(); await flush();
  assert.equal(s.plots.length, 0); assertAverages(s);
  assert.equal(s.element("[data-rate]").hidden, true);
  s.motion.matches = false; s.motion.dispatch("change"); await flush(); assert.equal(s.plots.length, 1);
  assertRate(s, 150);
  s.motion.matches = true; s.motion.dispatch("change");
  assert.equal(s.plots[0].destroyed, true); assert.equal(s.timers.size, 0);
  assert.equal(s.root.dataset.mounted, undefined);
  assertAverages(s);
  assert.equal(s.element("[data-rate]").hidden, true);
  assert.equal(s.element("[data-rate]").textContent, "");
  assert.equal(s.element("[data-elapsed]").textContent, "average");
  assert.equal(s.element("[data-replay]").hidden, true);
});

test("manual pause survives reduced-motion changes and leaves a working Play control", async t => {
  const s = setup(t); await s.visible(true);
  s.element("[data-replay]").dispatch("click");
  s.motion.matches = true; s.motion.dispatch("change");
  assert.equal(s.root.dataset.mounted, undefined);
  s.motion.matches = false; s.motion.dispatch("change"); await flush();
  assert.equal(s.root.dataset.mounted, "true");
  assertRate(s, 150);
  assert.equal(s.element("[data-replay]").hidden, false);
  assert.equal(s.element("[data-replay]").textContent, "Play");
  assert.equal(s.timers.size, 0);
  s.element("[data-replay]").dispatch("click"); assert.equal(s.timers.size, 1);
});

test("theme and size follow tokens and the container; teardown releases observers and timers", async t => {
  const s = setup(t); await s.visible(true); const plot = s.plots[0];
  assert.match(plot.opts.series[1].stroke(), /^dark:/);
  s.element("[data-replay]").dispatch("click");
  assert.equal(s.timers.size, 0, "manual pause still permits responsive/theme redraws");
  s.theme("light"); assert.match(plot.opts.series[1].stroke(), /^light:/);
  assert.match(plot.opts.axes[1].grid.stroke(), /^light:/);
  s.element("[data-plot]").getBoundingClientRect = () => ({ width: 320, height: 208 });
  s.observers.size.callback(); assert.equal(plot.width, 320); assert.equal(plot.height, 208);
  s.doc.hidden = true; s.doc.dispatch("visibilitychange");
  const redraws = plot.redraws;
  s.theme("dark"); s.observers.size.callback();
  assert.equal(plot.redraws, redraws, "hidden tabs do not redraw on theme or resize events");
  s.doc.hidden = false; s.doc.dispatch("visibilitychange");
  assert.match(plot.opts.series[1].stroke(), /^dark:/);
  assert.equal(s.timers.size, 0);
  s.cleanup(); assert.equal(s.timers.size, 0); assert.equal(plot.destroyed, true);
  Object.values(s.observers).forEach(observer => assert.equal(observer.disconnected, true));
  assert.equal(s.doc.listeners.get("visibilitychange").size, 0);
});

test("server markup retains accessible chart and reserves canvas/control/readout layout", () => {
  assert.match(source, /<rect data-reveal[^>]+width=\{right - left\}/);
  assert.match(source, /data-replay hidden aria-label="Pause/);
  assert.match(source, /aria-describedby=\{`\$\{id\}-description`\}/);
  assert.match(source, /<title[^>]+>.*formatRate\(sustained.accepted_per_second\)/);
  assert.match(source, /data-plot aria-hidden="true"/);
  assert.match(source, /aspect-ratio: 5 \/ 2/);
  assert.match(source, /font-variant-numeric: tabular-nums/);
  assert.match(source, /data-rate hidden style=\{`min-width: \$\{rateWidth\}ch`\}><\/span>/);
  assert.match(source, /const rateWidth = `· \$\{formatRate\(maximum\)\} items\/s, 5 s rate`\.length/);
  assert.match(source, /\.fo-replay-readout > span \{ white-space: nowrap;/);
  assert.match(source, /:focus-visible/);
  assert.match(client, /await import\("uplot"\)/);
  assert.doesNotMatch(source, /<script/);
  const landing = readFileSync(new URL("../src/content/docs/index.mdx", import.meta.url), "utf8");
  assert.match(landing, /<ThroughputReplayClient \/>/);
  assert.match(source, /development_preview && <p/);
});

test("server-rendered headline and legend use sustained averages that add up", () => {
  assert.equal(sustained.spans_per_second + sustained.log_records_per_second + sustained.metric_points_per_second,
    sustained.accepted_per_second);
  assert.match(source, /<strong data-counter>\{formatRate\(sustained.accepted_per_second\)\}<\/strong>/);
  for (const [signal, field] of [["spans", "spans_per_second"], ["logs", "log_records_per_second"], ["metrics", "metric_points_per_second"]]) {
    assert.ok(source.includes(`<b data-value="${signal}">{formatRate(sustained.${field})}</b>`));
  }
});
