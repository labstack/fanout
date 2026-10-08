import { act } from "react";
import { createRoot as realCreateRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { App } from "@modelcontextprotocol/ext-apps";
import { PanelApp } from "./panel-app";
import template from "../../panels.html?raw";
import { fixture, traceFixture, presets, presetFixture, assertPresetData } from "../../tests/fixtures";
const bridge = vi.hoisted(() => ({ app: null as unknown as App, connected: true, connectionError: null as Error | null, autoResize: undefined as boolean | undefined }));
vi.mock("@modelcontextprotocol/ext-apps/react", async () => {
  const { useEffect } = await import("react");
  return { useApp: ({ onAppCreated, autoResize }: { onAppCreated(app: App): void; autoResize?: boolean }) => {
    bridge.autoResize = autoResize;
    useEffect(() => { if (bridge.connected) onAppCreated(bridge.app); }, []);
    return { app: bridge.connected ? bridge.app : null, error: bridge.connectionError };
  } };
});
vi.mock("../dashboards/echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); bridge.connected = true; bridge.connectionError = null;
  bridge.app = { callServerTool: vi.fn(), sendSizeChanged: vi.fn(), getHostContext: () => ({ theme: "light" }) } as unknown as App;
});
afterEach(() => vi.unstubAllGlobals());
async function mount() {
  const parsed = new DOMParser().parseFromString(template, "text/html");
  expect(parsed.querySelector('script[type="module"]')?.getAttribute("src")).toBe("/src/mcp-apps/main.tsx");
  const node = document.importNode(parsed.getElementById("root")!, true); document.body.append(node); const root = realCreateRoot(node);
  await act(async () => root.render(<PanelApp />));
  return { node, root, async cleanup() { await act(async () => root.unmount()); node.remove(); } };
}
it.each([false, true])("renders panels.html fixtures with bridge theme changes and no host fetch (dark=%s)", async dark => {
  const fetch = vi.spyOn(globalThis, "fetch"); const view = await mount();
  try {
    expect(view.node.querySelector('[aria-label="Loading panel view"]')).not.toBeNull();
    await act(async () => { await bridge.app.ontoolresult!({ structuredContent: { ...fixture(), trace: traceFixture }, content: [] }); await bridge.app.onhostcontextchanged!({ theme: dark ? "dark" : "light" }); });
    expect(document.documentElement.getAttribute("data-mantine-color-scheme")).toBe(dark ? "dark" : "light");
    expect(view.node.querySelector('[data-panel="p"]')).not.toBeNull(); expect(view.node.textContent).toContain("No logs for checkout");
    expect(view.node.textContent).toContain("correlated failure"); expect(view.node.textContent).not.toMatch(/Explain|fix it|Duplicate|Remove panel|Copy link/);
    expect(fetch).not.toHaveBeenCalled(); expect(bridge.app.callServerTool).not.toHaveBeenCalled();
    await act(async () => { await bridge.app.onhostcontextchanged!({ theme: dark ? "light" : "dark" }); });
    expect(document.documentElement.getAttribute("data-mantine-color-scheme")).toBe(dark ? "light" : "dark");
  } finally { await view.cleanup(); fetch.mockRestore(); }
});
it.each([{ data: {} }, { ...fixture(), vars: { service: 123 } }, { ...fixture(), results: [] }])("fails closed with a sanitized error for malformed tool results", async structuredContent => {
  const view = await mount();
  try {
    await act(async () => { await bridge.app.ontoolresult!({ structuredContent, content: [] }); });
    expect(view.node.querySelector('[role="alert"]')?.textContent).toBe("This view could not be loaded. Please try again.");
    expect(view.node.querySelector('[data-panel]')).toBeNull();
  } finally { await view.cleanup(); }
});
it("sanitizes tool and bridge errors and recovers with a new valid fragment", async () => {
  const view = await mount();
  try {
    await act(async () => { await bridge.app.ontoolresult!({ isError: true, content: [{ type: "text", text: "private tool error" }] }); });
    expect(view.node.textContent).not.toContain("private");
    await act(async () => { await bridge.app.ontoolresult!({ structuredContent: fixture(), content: [] }); });
    expect(view.node.textContent).toContain("No logs for checkout");
    await act(async () => bridge.app.onerror!(new Error("private bridge error")));
    expect(view.node.querySelector('[role="alert"]')?.textContent).toBe("This view could not be refreshed. Please try again.");
    expect(view.node.textContent).toContain("No logs for checkout");
    expect(view.node.querySelector('[data-panel="p"]')).not.toBeNull();
  } finally { await view.cleanup(); }
});
it("waits for the bridge and sanitizes connection errors", async () => {
  bridge.connected = false;
  const view = await mount();
  try {
    expect(view.node.querySelector('[aria-label="Loading panel view"]')).not.toBeNull();
    bridge.connectionError = new Error("private connection error");
    await act(async () => view.root.render(<PanelApp />));
    expect(view.node.querySelector('[role="alert"]')?.textContent).toBe("This view could not be loaded. Please try again.");
  } finally { await view.cleanup(); }
});

it.each(presets.flatMap(preset => [false, true].map(dark => ({ preset, dark }))))("renders the real $preset preset through panels.html and PanelApp (dark=$dark)", async ({ preset, dark }) => {
  const fetch = vi.spyOn(globalThis, "fetch"); const view = await mount();
  try {
    const raw = presetFixture(preset);
    await act(async () => { await bridge.app.ontoolresult!({ structuredContent: raw, content: [] }); await bridge.app.onhostcontextchanged!({ theme: dark ? "dark" : "light" }); });
    expect(document.documentElement.getAttribute("data-mantine-color-scheme")).toBe(dark ? "dark" : "light");
    expect(view.node.querySelectorAll("[data-panel]")).toHaveLength(raw.dashboard.panels.length);
    assertPresetData(view.node, preset);
    expect(fetch).not.toHaveBeenCalled(); expect(bridge.app.callServerTool).not.toHaveBeenCalled();
  } finally { await view.cleanup(); fetch.mockRestore(); }
});

it("disables document autosizing and measures only an intrinsic content wrapper", async () => {
  const view = await mount();
  try {
    expect(bridge.autoResize).toBe(false);
    expect(view.node.querySelector("[data-app-content]")).not.toBeNull();
  } finally { await view.cleanup(); }
});

it.each([false,true])("negotiates host fullscreen only when advertised (available=%s)",async available=>{
 bridge.app.getHostContext=()=>({theme:"light",displayMode:"inline",availableDisplayModes:available?["inline","fullscreen"]:["inline"]});
 bridge.app.requestDisplayMode=vi.fn().mockResolvedValue({mode:"fullscreen"});
 const view=await mount();
 try {
  await act(async()=>bridge.app.ontoolresult!({structuredContent:fixture(),content:[]}));
  const menu=view.node.querySelector<HTMLButtonElement>('[aria-label="Checkout logs menu"]')!;
  await act(async()=>menu.click());
  const open=[...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el=>el.textContent==="View");
  if(!available){expect(open).toBeUndefined();expect(bridge.app.requestDisplayMode).not.toHaveBeenCalled();return;}
  await act(async()=>open!.click());
  expect(bridge.app.requestDisplayMode).toHaveBeenCalledWith({mode:"fullscreen"});
  expect(document.querySelector('[role="dialog"]')?.getAttribute("aria-label")).toBe("Checkout logs");
  await act(async()=>bridge.app.onhostcontextchanged!({displayMode:"fullscreen"}));
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(document.querySelector('[aria-label="Close panel view"]')).toBeNull();
  const focused = view.node.querySelector<HTMLElement>('[data-fragment-fullscreen]')!;
  expect(focused?.getAttribute("aria-label")).toBe("Checkout logs");
  expect(focused?.textContent).toContain("No logs for checkout");
  expect(view.node.querySelector('[aria-label="Dashboard variables"]')?.closest('[hidden]')).not.toBeNull();
  await act(async()=>bridge.app.onhostcontextchanged!({displayMode:"inline"}));
  await act(async()=>{await new Promise(r=>setTimeout(r,250));});
  expect(document.querySelector('[role="dialog"]')).toBeNull();expect(document.activeElement).toBe(menu);
  await act(async()=>menu.click());await act(async()=>[...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el=>el.textContent==="View")!.click());
  await act(async()=>bridge.app.onhostcontextchanged!({displayMode:"fullscreen"}));
  await act(async()=>view.node.querySelector<HTMLElement>('[data-fragment-fullscreen]')!.dispatchEvent(new KeyboardEvent("keydown",{key:"Escape",bubbles:true})));
  expect(bridge.app.requestDisplayMode).toHaveBeenLastCalledWith({mode:"inline"});
  await act(async()=>bridge.app.onhostcontextchanged!({displayMode:"inline"}));
  await act(async()=>{await new Promise(r=>setTimeout(r,50));});
  expect(view.node.querySelector('[data-fragment-fullscreen]')).toBeNull();expect(document.activeElement).toBe(menu);
  expect(bridge.app.callServerTool).not.toHaveBeenCalled();
 } finally {await view.cleanup();}
});
