import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, QueryObserver } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, type DashboardRecord, type VersionRecord } from "./api";
import { HistoryDrawer } from "./history";
import { fanoutTheme } from "../theme";
import { panelResultsKey, variableOptionsKey } from "./query-keys";

const api = vi.hoisted(() => ({ listVersions: vi.fn(), getVersion: vi.fn(), restoreVersion: vi.fn() }));
vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), ...api }));
const record: DashboardRecord = { id: "board", name: "Current", description: "", version: 3, is_default: false, created_at: "2026-10-01T12:00:00Z", updated_at: "2026-10-08T12:00:00Z", spec: { version: 1, name: "Current", time: { range: "1h" }, panels: [{ id: "note", title: "Note", viz: "text", content: "Current content" }] } };
const historic = (version: number): VersionRecord => ({ dashboard: { ...record, version, name: `Old ${version}`, spec: { ...record.spec, name: `Old ${version}`, panels: [{ ...record.spec.panels[0], content: `Historical content ${version}` }] } }, author_kind: "agent", author_id: "opaque", message: "Updated note", created_at: record.created_at, changes: [{ panel_id: "note", title: "Note", kind: "changed", fields: ["title"] }], layout_changed: true, dashboard_fields: ["name"], changes_available: true });
let root: Root, client: QueryClient;
let render: (opened?: boolean, ready?: () => void) => Promise<void>;
let scheme: "light" | "dark";
let currentVersion: number;
let onRestored: ReturnType<typeof vi.fn<(record: DashboardRecord) => void>>, onClose: ReturnType<typeof vi.fn<() => void>>;
const button = (label: string) => [...document.querySelectorAll("button")].find(b => b.textContent === label)!;
const settle = async (ready?: () => void) => {
  let idle = 0;
  await vi.waitFor(async () => {
    await act(async () => {});
    if (ready) { ready(); return; }
    idle = client.isFetching() === 0 && client.isMutating() === 0 ? idle + 1 : 0;
    expect(idle).toBeGreaterThanOrEqual(2);
    expect(document.body.textContent).not.toMatch(/Loading (version|history)/);
  });
};
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }

beforeEach(() => {
  scheme = "light";
  currentVersion = 3;
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  api.listVersions.mockReset().mockResolvedValue([
    { version: 3, author_kind: "user", author_id: "opaque-user", message: "Person edit", created_at: record.updated_at },
    { version: 2, author_kind: "agent", author_id: "opaque-agent", message: "Agent edit", created_at: record.created_at },
    { version: 1, author_kind: "system", message: "Created", created_at: record.created_at },
  ]);
  api.getVersion.mockReset().mockImplementation((_id, version) => Promise.resolve(historic(version)));
  api.restoreVersion.mockReset();
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  onRestored = vi.fn(); onClose = vi.fn();
  const host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  render = async (opened = true, ready?: () => void) => { await act(async () => root.render(<MantineProvider theme={fanoutTheme} forceColorScheme={scheme}><QueryClientProvider client={client}><HistoryDrawer id="board" currentVersion={currentVersion} opened={opened} onClose={onClose} onRestored={onRestored} /></QueryClientProvider></MantineProvider>)); if (opened) await settle(ready); };
});
afterEach(async () => { await act(async () => root.unmount()); client.clear(); document.body.innerHTML = ""; vi.unstubAllGlobals(); });

it.each(["light", "dark"] as const)("queries only while open and shows authors, messages, absolute titles and server changes with a read-only spec in %s", async theme => {
  scheme = theme;
  await render(false); expect(api.listVersions).not.toHaveBeenCalled();
  await render();
  expect(document.body.textContent).toContain("You"); expect(document.body.textContent).toContain("Agent"); expect(document.body.textContent).toContain("System");
  expect(document.body.textContent).toContain("Person edit"); expect(document.body.textContent).toContain("Agent edit"); expect(document.body.textContent).not.toContain("opaque-user");
  expect(document.querySelector("time")?.title).toBeTruthy();
  await act(async () => button("Version 2").click()); await settle();
  expect(document.body.textContent).toContain("Old 2"); expect(document.body.textContent).toContain("~ note.title");
  expect(document.body.textContent?.match(/Layout adjusted/g)).toHaveLength(1);
  expect(document.body.textContent).toContain("Dashboard: name");
  await act(async () => button("Spec").click());
  expect(document.body.textContent).toContain("Historical content 2");
  expect(client.getQueryData(["dashboard", "board"])).toBeUndefined();
  expect(document.querySelector("textarea,input")).toBeNull();
});

it("aborts prior selection and closing requests and never displays a late historical response", async () => {
  const first = deferred<VersionRecord>(), second = deferred<VersionRecord>();
  const signals: AbortSignal[] = [];
  api.getVersion.mockImplementation((_id, version, signal) => { signals.push(signal); return version === 3 ? first.promise : second.promise; });
  await render(true, () => expect(document.body.textContent).toContain("Loading version"));
  await act(async () => button("Version 2").click()); await settle(() => expect(signals[0].aborted).toBe(true)); expect(signals[0].aborted).toBe(true);
  await act(async () => second.resolve(historic(2))); await settle();
  await act(async () => first.resolve(historic(3))); await settle();
  expect(document.body.textContent).toContain("Old 2"); expect(document.body.textContent).not.toContain("Old 3");
  api.getVersion.mockReturnValue(new Promise(() => {}));
  await act(async () => button("Version 1").click()); await settle(() => expect(api.getVersion.mock.calls.at(-1)?.[1]).toBe(1));
  await render(false); expect(signals[1].aborted).toBe(false);
  // The newly selected request is canceled on close as well.
  expect(api.getVersion.mock.calls.at(-1)?.[2].aborted).toBe(true);
});

it("submits one restore for a double click, retains the pending lock when reopened and refreshes all current caches", async () => {
  const pending = deferred<DashboardRecord>(); api.restoreVersion.mockReturnValue(pending.promise);
  client.setQueryData(["dashboard", "board"], record);
  const panelKey = panelResultsKey("board", record.spec, record.spec.time, {}, false);
  const variablesKey = variableOptionsKey("dashboard-board", record.spec, record.spec.time, {});
  for (const key of [["dashboards"], panelKey, variablesKey]) client.setQueryData(key, ["stale"]);
  await render(); await act(async () => button("Version 1").click()); await settle();
  const restore = button("Restore version 1");
  await act(async () => { restore.click(); restore.click(); });
  await settle(() => expect(restore.disabled).toBe(true));
  expect(api.restoreVersion).toHaveBeenCalledTimes(1); expect(api.restoreVersion).toHaveBeenCalledWith("board", 1); expect(restore.disabled).toBe(true);
  await render(false); await render(true, () => expect(button("Restore version 1")?.disabled).toBe(true));
  const restored = { ...record, version: 4, spec: historic(1).dashboard.spec };
  await act(async () => pending.resolve(restored)); await settle();
  expect(client.getQueryData(["dashboard", "board"])).toEqual(restored);
  expect(client.getQueryState(["dashboards"])?.isInvalidated).toBe(true);
  expect(client.getQueryState(panelKey)?.isInvalidated).toBe(true);
  expect(client.getQueryState(variablesKey)?.isInvalidated).toBe(true);
  expect(api.listVersions.mock.calls.length).toBeGreaterThan(1);
  expect(onRestored).toHaveBeenCalledWith(restored); expect(document.body.textContent).not.toContain("Restored v1 as v4");
});

it.each([404, 409, 500])("keeps the current view on restore failure %s and retries only after a new click", async status => {
  api.restoreVersion.mockRejectedValue(new ApiError("Restore failed", status)); client.setQueryData(["dashboard", "board"], record);
  await render(); await act(async () => button("Version 1").click()); await settle();
  await act(async () => button("Restore version 1").click()); await settle();
  expect(document.body.textContent).toContain("Restore failed"); expect(api.restoreVersion).toHaveBeenCalledTimes(1); expect(onRestored).not.toHaveBeenCalled();
  expect(client.getQueryData(["dashboard", "board"])).toEqual(record);
  await act(async () => button("Restore version 1").click()); await settle(); expect(api.restoreVersion).toHaveBeenCalledTimes(2);
});

it("shows list loading, empty and error states with a deliberate read retry", async () => {
  const pending = deferred<[]>(); api.listVersions.mockReturnValue(pending.promise);
  await render(true, () => expect(document.body.textContent).toContain("Loading history"));
  await act(async () => pending.resolve([])); await settle(); expect(document.body.textContent).toContain("No versions available");
  await render(false); client.removeQueries({ queryKey: ["dashboard-versions", "board"] });
  api.listVersions.mockRejectedValue(new Error("History failed")); await render(); expect(document.body.textContent).toContain("History failed");
  api.listVersions.mockResolvedValue([]); await act(async () => button("Retry history").click()); await settle(() => expect(document.body.textContent).toContain("No versions available")); expect(document.body.textContent).toContain("No versions available");
});

it("shows not-found and pruned-predecessor states without invented changes", async () => {
  api.getVersion.mockRejectedValue(new ApiError("Missing", 404)); await render();
  expect(document.body.textContent).toContain("This version is no longer available"); expect(button("Restore version 3")).toBeUndefined();
  api.getVersion.mockResolvedValue({ ...historic(2), changes: [], layout_changed: false, dashboard_fields: [], changes_available: false });
  await act(async () => button("Version 2").click()); await settle();
  expect(document.body.textContent).toContain("Changes unavailable"); expect(document.body.textContent).toContain("Old 2"); expect(button("Restore version 2").disabled).toBe(false);
});

it("uses Mantine Escape dismissal and native keyboard buttons", async () => {
  await render(); const choice = button("Version 2"); expect(choice.tagName).toBe("BUTTON");
  choice.focus(); expect(document.activeElement).toBe(choice);
  await act(async () => choice.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(onClose).toHaveBeenCalled();
});

it("offers a disabled Current version label and uses shared receipt layout chips", async () => {
  await render();
  expect(button("Current version")?.disabled).toBe(true);
  expect(button("Restore version 3")).toBeUndefined();
  expect([...document.querySelectorAll("[data-edit-chips] .mantine-Badge-label")].map(node => node.textContent)).toContain("Layout adjusted");
});

it("uses the receipt's Layout adjusted chip for the server layout flag", async () => {
  await render();
  expect([...document.querySelectorAll("[data-edit-chips] .mantine-Badge-label")].map(node => node.textContent)).toContain("Layout adjusted");
});

it("scopes escaped validation problems to the failed version and clears feedback on selection or close", async () => {
  api.restoreVersion.mockRejectedValue(new ApiError("Invalid spec", 400, [{ path: "panels[0].query", message: "<img src=x onerror=alert(1)>", hint: "Use an available column" }]));
  await render(); await act(async () => button("Version 1").click()); await settle();
  await act(async () => button("Restore version 1").click()); await settle();
  expect(document.body.textContent).toContain("Restore of version 1 failed");
  expect(document.body.textContent).toContain("panels[0].query: <img src=x onerror=alert(1)>");
  expect(document.body.textContent).toContain("Use an available column");
  expect(document.querySelector("img")).toBeNull(); expect(document.body.textContent).not.toContain("Try again");
  await act(async () => button("Version 2").click()); await settle();
  expect(document.body.textContent).not.toContain("Invalid spec");
  await act(async () => button("Restore version 2").click()); await settle();
  await render(false); await render(); expect(document.body.textContent).not.toContain("Invalid spec");
});

it.each([404, 409, 500])("gives status-specific restore advice for %s and refreshes pruned history", async status => {
  api.restoreVersion.mockRejectedValue(new ApiError("Rejected restore", status));
  await render(); await act(async () => button("Version 1").click()); await settle();
  const reads = api.listVersions.mock.calls.length;
  await act(async () => button("Restore version 1").click()); await settle();
  expect(document.body.textContent?.includes("Try again")).toBe(status === 500);
  if (status < 500) expect(document.body.textContent).toContain("Reload history");
  if (status === 404) expect(api.listVersions.mock.calls.length).toBeGreaterThan(reads);
});

it.each(["light", "dark"] as const)("uses the defined ok palette for success and clears it on close in %s", async theme => {
  scheme = theme; api.restoreVersion.mockResolvedValue({ ...record, version: 4 });
  await render(); await act(async () => button("Version 1").click()); await settle();
  await act(async () => button("Restore version 1").click()); await settle();
  const alert = [...document.querySelectorAll<HTMLElement>(".mantine-Alert-root")].find(node => node.textContent?.includes("Restored v1 as v4"))!;
  expect(alert.style.getPropertyValue("--alert-bg")).toBe("var(--mantine-color-ok-light)");
  expect(alert.style.getPropertyValue("--alert-color")).toBe("var(--mantine-color-ok-light-color)");
  await render(false); await render(); expect(document.body.textContent).not.toContain("Restored v1 as v4");
});

it("releases restore pending when the POST responds without waiting for or refetching the old panel batch", async () => {
  const refetch = vi.fn(() => new Promise<never>(() => {}));
  const observer = new QueryObserver(client, { queryKey: panelResultsKey("board", record.spec, record.spec.time, {}, false), queryFn: refetch, staleTime: Infinity, initialData: [] });
  const unsubscribe = observer.subscribe(() => {});
  try {
    api.restoreVersion.mockResolvedValue({ ...record, version: 4 });
    await render(); await act(async () => button("Version 1").click()); await settle();
    await act(async () => button("Restore version 1").click()); await settle();
    expect(button("Restore version 1").disabled).toBe(false);
    expect(client.isMutating()).toBe(0); expect(refetch).not.toHaveBeenCalled();
  } finally { unsubscribe(); }
});

it("refreshes the list when the current board version changes elsewhere", async () => {
  await render(); const reads = api.listVersions.mock.calls.length;
  currentVersion = 4; api.listVersions.mockResolvedValue([{ version: 4, author_kind: "agent", message: "Chat restore", created_at: record.updated_at }]);
  await render(); await settle();
  expect(api.listVersions.mock.calls.length).toBeGreaterThan(reads);
  expect(button("Version 4")).toBeDefined();
});

it("keeps content during the exit transition and exposes a named list with short live status", async () => {
  await render();
  expect(document.querySelector('[role="list"][aria-label="Saved versions"]')).not.toBeNull();
  expect(document.querySelectorAll('[role="listitem"]')).toHaveLength(3);
  const live = document.querySelector('[aria-live="polite"]');
  expect(live?.textContent?.length).toBeLessThan(100);
  await render(false); expect(document.querySelector("[data-version-history]")).not.toBeNull();
});
