import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { hasProductVideo } from "../src/components/landing/landing-media.ts";
import { mountProductVideo } from "../src/components/landing/product-video.ts";

const fixture = JSON.parse(readFileSync(new URL("./fixtures/landing-media.json", import.meta.url), "utf8"));

function events(value = {}) {
  const listeners = new Map();
  return { ...value, listeners,
    addEventListener(type, callback) { if (!listeners.has(type)) listeners.set(type, new Set()); listeners.get(type).add(callback); },
    removeEventListener(type, callback) { listeners.get(type)?.delete(callback); },
    dispatch(type) { for (const callback of listeners.get(type) ?? []) callback(); },
  };
}

function setup(t, reduced = false, manifest = fixture) {
  const players = {}, observers = {};
  const slots = ["light", "dark"].map(theme => ({ dataset: { videoTheme: theme },
    querySelector: () => ({ content: { firstElementChild: { cloneNode: () => {
      const player = events({ paused: true, plays: 0, pauses: 0, sources: manifest[`chat-${theme}`],
        play() { this.paused = false; this.plays++; return Promise.resolve(); },
        pause() { this.paused = true; this.pauses++; },
      });
      players[theme] = player;
      return player;
    } } } }), append() {},
  }));
  const control = events({ hidden: true, attributes: {}, setAttribute(key, value) { this.attributes[key] = value; } });
  const root = { querySelectorAll: () => slots, querySelector: selector => selector === "template" ? (hasProductVideo(manifest, "chat") ? {} : null) : control };
  const doc = events({ hidden: false, documentElement: { dataset: { theme: "dark" } } });
  const motion = events({ matches: reduced });
  const globals = { document: doc, matchMedia: () => motion,
    IntersectionObserver: class { constructor(callback) { this.callback = callback; observers.intersection = this; } observe() {} disconnect() { this.disconnected = true; } },
    MutationObserver: class { constructor(callback) { this.callback = callback; observers.theme = this; } observe() {} disconnect() { this.disconnected = true; } },
  };
  const restore = [];
  for (const [key, value] of Object.entries(globals)) {
    const original = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, { configurable: true, value });
    restore.push(() => original ? Object.defineProperty(globalThis, key, original) : delete globalThis[key]);
  }
  const cleanup = mountProductVideo(root);
  t.after(() => { cleanup(); restore.reverse().forEach(fn => fn()); });
  return { players, slots, control, motion, doc, observers, cleanup,
    visible(value) { observers.intersection.callback([{ isIntersecting: value }]); },
    theme(value) { doc.documentElement.dataset.theme = value; observers.theme.callback(); },
  };
}

test("poster-only slots need no playback observers", () => {
  assert.doesNotThrow(() => mountProductVideo({ querySelectorAll: () => [], querySelector: () => null })());
});

test("an incomplete manifest leaves posters without observers or players", t => {
  const manifest = structuredClone(fixture);
  delete manifest["chat-dark"].webm;
  const s = setup(t, false, manifest);
  assert.deepEqual(s.observers, {});
  assert.deepEqual(s.players, {});
  assert.equal(s.control.hidden, true);
});

test("only the visible theme starts, and hidden tabs and offscreen clips pause", t => {
  const s = setup(t);
  assert.deepEqual(s.players, {});
  s.visible(true);
  assert.equal(s.players.dark.muted, true);
  assert.equal(s.players.dark.paused, false);
  assert.deepEqual(s.players.dark.sources, fixture["chat-dark"]);
  assert.equal(s.players.light, undefined);
  s.players.dark.dispatch("playing");
  assert.equal(s.slots[1].dataset.playing, "");
  s.theme("light");
  assert.equal(s.players.dark.paused, true);
  assert.equal(s.slots[1].dataset.playing, undefined);
  assert.equal(s.players.light.paused, false);
  assert.deepEqual(s.players.light.sources, fixture["chat-light"]);
  s.doc.hidden = true; s.doc.dispatch("visibilitychange");
  assert.equal(s.players.light.paused, true);
  s.doc.hidden = false; s.doc.dispatch("visibilitychange");
  assert.equal(s.players.light.paused, false);
  s.visible(false);
  assert.equal(s.players.light.paused, true);
});

test("reduced motion never instantiates autoplay media and switches back to posters", t => {
  const s = setup(t, true);
  s.visible(true);
  assert.deepEqual(s.players, {});
  assert.equal(s.control.hidden, true);
  s.motion.matches = false; s.motion.dispatch("change");
  s.players.dark.dispatch("playing");
  assert.equal(s.slots[1].dataset.playing, "");
  s.motion.matches = true; s.motion.dispatch("change");
  assert.equal(s.players.dark.paused, true);
  assert.equal(s.slots[1].dataset.playing, undefined);
  s.players.dark.dispatch("playing");
  assert.equal(s.slots[1].dataset.playing, undefined);
});

test("manual pause survives visibility, theme and motion changes; teardown stops every player", t => {
  const s = setup(t);
  s.visible(true); s.control.dispatch("click");
  assert.equal(s.players.dark.paused, true);
  assert.equal(s.control.attributes["aria-label"], "Play clip");
  s.visible(false); s.visible(true); s.theme("light");
  s.motion.matches = true; s.motion.dispatch("change");
  s.motion.matches = false; s.motion.dispatch("change");
  assert.equal(s.players.light, undefined);
  s.control.dispatch("click");
  assert.equal(s.players.light.paused, false);
  s.cleanup();
  assert.equal(s.players.light.paused, true);
  assert.equal(s.players.dark.paused, true);
  for (const observer of Object.values(s.observers)) assert.equal(observer.disconnected, true);
  assert.equal(s.doc.listeners.get("visibilitychange").size, 0);
  assert.equal(s.motion.listeners.get("change").size, 0);
});
