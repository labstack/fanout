import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ColorSchemeToggle } from "./shell";

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
});
