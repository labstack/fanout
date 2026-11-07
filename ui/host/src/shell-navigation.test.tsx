import { MantineProvider } from "@mantine/core";
import { within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn(), useParams: () => ({}), useRouterState: () => "/dashboards" }));
vi.mock("./app-context", () => ({ createDashboardPrompt: "", useFanoutApp: () => ({ agentAvailable: false, threadID: "", threadMissing: false, newThread: vi.fn(), selectThread: vi.fn(), openChat: vi.fn() }) }));
vi.mock("./auth", () => ({ logout: vi.fn(), useViewer: () => ({ id: "u", email: "u@example.test", display_name: "U", status: "active", role: "admin" }) }));
vi.mock("./rail", () => ({ default: ({ onNewDashboard }: { onNewDashboard(): void }) => <button onClick={onNewDashboard}>New dashboard</button> }));
vi.mock("./dashboards/new-dashboard", () => ({ NewDashboard: ({ onClose }: { onClose(): void }) => <div role="dialog" aria-label="New dashboard"><button onClick={onClose}>Cancel</button></div> }));
import Shell from "./shell";

const cleanups: (() => void)[] = [];
const burgerButton = () => document.querySelector<HTMLButtonElement>(".mantine-Burger-root")!;
let mobile = false;
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: mobile && query.includes("max-width"), media: query, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false }));
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; mobile = false; });

async function render() {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  await act(async () => root.render(<MantineProvider><Shell><p>Page</p></Shell></MantineProvider>));
  cleanups.push(() => root.unmount());
  const ui = within(document.body);
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  return { ui, wait };
}

describe("Shell navigation", () => {
  it("collapses and expands the desktop navigation", async () => {
    const { ui } = await render();
    const collapse = ui.getByRole("button", { name: "Collapse navigation" });
    expect(collapse.getAttribute("aria-expanded")).toBe("true");
    await act(async () => collapse.click());
    expect(ui.getByRole("button", { name: "Expand navigation" }).getAttribute("aria-expanded")).toBe("false");
    await act(async () => ui.getByRole("button", { name: "Expand navigation" }).click());
    expect(ui.getByRole("button", { name: "Collapse navigation" })).toBeTruthy();
  });

  it("offers a skip link to the page and says whether the navigation is open", async () => {
    const { ui, wait } = await render();
    const skip = ui.getByRole("link", { name: "Skip to content" });
    expect(document.querySelector(skip.getAttribute("href")!)?.textContent).toBe("Page");
    const burger = burgerButton();
    expect(burger.getAttribute("aria-expanded")).toBe("false");
    await act(async () => burger.click());
    await wait(() => expect(burgerButton().getAttribute("aria-expanded")).toBe("true"));
    expect(burgerButton().getAttribute("aria-label")).toBe("Close navigation");
  });

  it("closes the mobile navigation before New dashboard opens, and returns focus when it closes", async () => {
    mobile = true;
    const { ui, wait } = await render();
    const burger = burgerButton();
    await act(async () => burger.click());
    await wait(() => expect(ui.getAllByRole("button", { name: "New dashboard", hidden: true })).toHaveLength(2));
    const inDrawer = ui.getAllByRole("button", { name: "New dashboard", hidden: true })[1]!;
    inDrawer.focus();
    await act(async () => inDrawer.click());
    await wait(() => expect(ui.getByRole("dialog", { name: "New dashboard" })).toBeTruthy());
    expect(burger.getAttribute("aria-expanded")).toBe("false");
    await act(async () => ui.getByRole("button", { name: "Cancel" }).click());
    await wait(() => expect(document.activeElement).toBe(burgerButton()));
  });
});
