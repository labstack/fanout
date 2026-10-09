import { act, useRef } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { useShortcutPreference } from "./shortcut-preference";
import { useShortcuts } from "./use-shortcuts";

it("persists disabled single-key shortcuts per viewer while visible controls remain usable", async () => {
  localStorage.clear();
  const el = document.createElement("div"); document.body.append(el);
  const root = createRoot(el), refresh = vi.fn();
  function Host({viewer}: {viewer: string}) {
    const preference = useShortcutPreference(viewer), region = useRef<HTMLDivElement>(null);
    useShortcuts(region, {r: refresh}, {enabled: preference.enabled});
    return <div ref={region}><button onClick={() => preference.change(!preference.enabled)}>Single-key shortcuts</button><button onClick={refresh}>Refresh</button></div>;
  }
  const draw = async (viewer: string, key: number) => act(async () => root.render(<Host viewer={viewer} key={key}/>));
  const press = async () => act(async () => document.body.dispatchEvent(new KeyboardEvent("keydown", {key: "r", bubbles: true, cancelable: true})));
  try {
    await draw("alice", 1); await press(); expect(refresh).toHaveBeenCalledOnce();
    await act(async () => el.querySelector("button")!.click());
    await press(); expect(refresh).toHaveBeenCalledOnce();
    await draw("alice", 2); await press(); expect(refresh).toHaveBeenCalledOnce();
    await act(async () => el.querySelectorAll("button")[1].click()); expect(refresh).toHaveBeenCalledTimes(2);
    await draw("bob", 3); await press(); expect(refresh).toHaveBeenCalledTimes(3);
    expect(localStorage.getItem("fanout:single-key-shortcuts:alice")).toBe("off");
    expect(localStorage.getItem("fanout:single-key-shortcuts:bob")).toBeNull();
  } finally { await act(async () => root.unmount()); el.remove(); localStorage.clear(); }
});
