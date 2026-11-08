import { MantineProvider } from "@mantine/core";
import { act, useCallback, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ColorSchemeToggle } from "./color-scheme-toggle";
import { ShortcutsHelp } from "./dashboards/shortcuts-help";
import { HelpMenu } from "./help-menu";
import { useShortcutsHandler } from "./shortcuts-event";

async function mount() {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(<MantineProvider><ColorSchemeToggle /></MantineProvider>));
  return { root, button: document.querySelector<HTMLButtonElement>('button[aria-label^="Switch to"]')! };
}

const tooltip = () => document.querySelectorAll('.mantine-Tooltip-tooltip, [role="tooltip"]').length;

describe("colour scheme toggle", () => {
  afterEach(() => { document.body.innerHTML = ""; });

  it("closes its tooltip on the click that makes it wrong", async () => {
    const { root, button } = await mount();
    await act(async () => { button.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })); });
    expect(tooltip()).toBe(1);

    await act(async () => { button.click(); });
    await vi.waitFor(() => expect(tooltip()).toBe(0));
    await act(async () => root.unmount());
  });

  it("still answers to keyboard focus, which taking control of the tooltip could have broken", async () => {
    const { root, button } = await mount();
    await act(async () => { button.dispatchEvent(new FocusEvent("focus", { bubbles: false })); button.dispatchEvent(new FocusEvent("focusin", { bubbles: true })); });
    expect(tooltip()).toBe(1);
    await act(async () => { button.dispatchEvent(new FocusEvent("blur", { bubbles: false })); button.dispatchEvent(new FocusEvent("focusout", { bubbles: true })); });
    await vi.waitFor(() => expect(tooltip()).toBe(0));
    await act(async () => root.unmount());
  });
});

describe("help menu", () => {
  afterEach(() => { document.body.innerHTML = ""; });

  function Page({ registered }: { registered: boolean }) {
    const [opened, setOpened] = useState(false);
    return <>
      <HelpMenu />
      {registered && <Registered open={useCallback(() => setOpened(true), [])} />}
      <ShortcutsHelp opened={opened} onClose={() => setOpened(false)} enabled onEnabled={() => {}} />
    </>;
  }
  function Registered({ open }: { open(): void }) { useShortcutsHandler(open); return null; }

  async function mountHelp(registered: boolean) {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><Page registered={registered} /></MantineProvider>));
    const help = document.querySelector<HTMLButtonElement>('button[aria-label="Help"]')!;
    await act(async () => { help.focus(); help.click(); });
    await vi.waitFor(() => expect(document.querySelector('[role="menu"]')).not.toBeNull());
    return { root, help };
  }
  const item = () => [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find(element => element.textContent?.includes("Keyboard shortcuts"));

  it("offers keyboard shortcuts only while a page has registered them", async () => {
    const { root } = await mountHelp(false);
    expect(item()).toBeUndefined();
    await act(async () => root.unmount());
  });

  it("opens the shortcuts dialog with focus inside it and returns focus to Help on Escape", async () => {
    const { root, help } = await mountHelp(true);
    await act(async () => { item()!.click(); });
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')?.textContent).toContain("Keyboard shortcuts"));
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')!.contains(document.activeElement)).toBe(true));
    await act(async () => { document.activeElement!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeNull());
    await vi.waitFor(() => expect(document.activeElement).toBe(help));
    await act(async () => root.unmount());
  });
});
