import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RangeMenu, Toolbar } from "./toolbar";

let root: Root;
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(async () => {
  await act(async () => root?.unmount());
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

async function render() {
  const host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  const onRange = vi.fn();
  const onAbsolute = vi.fn();
  const onHistory = vi.fn();
  await act(async () => {
    root.render(<MantineProvider><Toolbar time={{ range: "1h" }} refresh="off" compare={false} editing={false} fetching={false} updatedAt={null}
      onRange={onRange} onAbsolute={onAbsolute} onZoomOut={vi.fn()} onRefresh={vi.fn()} onRefreshNow={vi.fn()} onCompare={vi.fn()} onEdit={vi.fn()} onHistory={onHistory} /></MantineProvider>);
  });
  const trigger = [...host.querySelectorAll("button")].find((button) => button.textContent === "Last hour")!;
  await act(async () => trigger.click());
  expect(trigger.getAttribute("aria-expanded")).toBe("true");
  return { trigger, onRange, onAbsolute, onHistory };
}

describe("dashboard range menu", () => {
  it("opens history through an accessible toolbar button", async () => {
    const { onHistory } = await render();
    const history = [...document.querySelectorAll("button")].find(button => button.textContent === "History")!;
    await act(async () => history.click());
    expect(onHistory).toHaveBeenCalledTimes(1);
    expect(history.closest('[role="group"]')?.getAttribute("aria-label")).toBe("Dashboard controls");
  });
  it("closes after choosing a relative range", async () => {
    const { trigger, onRange } = await render();
    const choice = [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((item) => item.textContent === "Last 6 hours")!;
    await act(async () => choice.click());
    expect(onRange).toHaveBeenCalledWith("6h");
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
  });

  it("closes after applying an absolute range", async () => {
    const { trigger, onAbsolute } = await render();
    const from = document.querySelector<HTMLInputElement>('[aria-label="From"]')!;
    const to = document.querySelector<HTMLInputElement>('[aria-label="To"]')!;
    await act(async () => {
      for (const [input, value] of [[from, "2026-10-01T12:00"], [to, "2026-10-01T13:00"]] as const) {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
        input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
        input.dispatchEvent(new Event("change", { bubbles: true }));
      }
    });
    const apply = [...document.querySelectorAll("button")].find((button) => button.textContent === "Apply range")!;
    expect(apply.disabled).toBe(false);
    await act(async () => apply.click());
    expect(onAbsolute).toHaveBeenCalledWith(new Date(from.value).toISOString(), new Date(to.value).toISOString());
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
  });
});


describe("range picker placement", () => {
  const phone = (matches: boolean) => vi.stubGlobal("matchMedia", (query: string) => ({ matches: matches && query.includes("max-width"), media: query, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false }));
  async function place(inHeader: boolean) {
    const slot = document.createElement("div"); slot.id = "shell-time-range"; document.body.append(slot);
    const host = document.createElement("div"); document.body.append(host);
    root = createRoot(host);
    await act(async () => root.render(<MantineProvider><RangeMenu inHeader={inHeader} time={{ range: "1h" }} onRange={vi.fn()} onAbsolute={vi.fn()} /></MantineProvider>));
    return { inSlot: slot.querySelector("button") !== null, inPage: host.querySelector("button") !== null };
  }

  it("moves a page's picker into the header", async () => {
    phone(false);
    expect(await place(true)).toEqual({ inSlot: true, inPage: false });
  });
  it("keeps a dialog's picker inline", async () => {
    phone(false);
    expect(await place(false)).toEqual({ inSlot: false, inPage: true });
  });
  it("keeps a page's picker in the page on a phone", async () => {
    phone(true);
    expect(await place(true)).toEqual({ inSlot: false, inPage: true });
  });
});
