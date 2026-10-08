import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, type DashboardRecord, type VersionRecord } from "./api";
import { HistoryDrawer } from "./history";
import { fanoutTheme } from "../theme";

const api = vi.hoisted(() => ({ listVersions: vi.fn(), getVersion: vi.fn(), restoreVersion: vi.fn() }));
vi.mock("./api", async (original) => ({ ...await original<typeof import("./api")>(), ...api }));
const record: DashboardRecord = { id: "board", name: "Current", description: "", version: 3, is_default: false, created_at: "2026-10-01T12:00:00Z", updated_at: "2026-10-08T12:00:00Z", spec: { version: 1, name: "Current", time: { range: "1h" }, panels: [{ id: "note", title: "Note", viz: "text", content: "Current content" }] } };
const historic = (version: number): VersionRecord => ({ dashboard: { ...record, version, name: `Old ${version}`, spec: { ...record.spec, name: `Old ${version}`, panels: [{ ...record.spec.panels[0], content: `Historical content ${version}` }] } }, author_kind: "agent", author_id: "opaque", message: "Updated note", created_at: record.created_at, changes: [{ panel_id: "note", title: "Note", kind: "changed", fields: ["title"] }], layout_changed: true, dashboard_fields: ["name"], changes_available: true });
let root: Root, client: QueryClient;
let render: (opened?: boolean) => Promise<void>;
let scheme: "light" | "dark";
let onRestored: ReturnType<typeof vi.fn<(record: DashboardRecord) => void>>, onClose: ReturnType<typeof vi.fn<() => void>>;
const button = (label: string) => [...document.querySelectorAll("button")].find(b => b.textContent === label)!;
const settle = async () => { await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); }); };
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }

beforeEach(() => {
  scheme = "light";
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
  render = async (opened = true) => { await act(async () => root.render(<MantineProvider theme={fanoutTheme} forceColorScheme={scheme}><QueryClientProvider client={client}><HistoryDrawer id="board" currentVersion={3} opened={opened} onClose={onClose} onRestored={onRestored} /></QueryClientProvider></MantineProvider>)); await settle(); };
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
  expect(document.body.textContent?.match(/layout changed/g)).toHaveLength(1);
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
  await render(); expect(document.body.textContent).toContain("Loading version");
  await act(async () => button("Version 2").click()); await settle(); expect(signals[0].aborted).toBe(true);
  await act(async () => second.resolve(historic(2))); await settle();
  await act(async () => first.resolve(historic(3))); await settle();
  expect(document.body.textContent).toContain("Old 2"); expect(document.body.textContent).not.toContain("Old 3");
  api.getVersion.mockReturnValue(new Promise(() => {}));
  await act(async () => button("Version 1").click()); await settle();
  await render(false); expect(signals[1].aborted).toBe(false);
  // The newly selected request is canceled on close as well.
  expect(api.getVersion.mock.calls.at(-1)?.[2].aborted).toBe(true);
});

it("submits one restore for a double click, retains the pending lock when reopened and refreshes all current caches", async () => {
  const pending = deferred<DashboardRecord>(); api.restoreVersion.mockReturnValue(pending.promise);
  client.setQueryData(["dashboard", "board"], record);
  const panelKey = ["panels", JSON.stringify(["board", "content"])];
  const variablesKey = ["variables", "dashboard-board", "content"];
  for (const key of [["dashboards"], panelKey, variablesKey]) client.setQueryData(key, ["stale"]);
  await render(); await act(async () => button("Version 1").click()); await settle();
  const restore = button("Restore version 1");
  await act(async () => { restore.click(); restore.click(); });
  await settle();
  expect(api.restoreVersion).toHaveBeenCalledTimes(1); expect(api.restoreVersion).toHaveBeenCalledWith("board", 1); expect(restore.disabled).toBe(true);
  await render(false); await render(); expect(button("Restore version 1").disabled).toBe(true);
  const restored = { ...record, version: 4, spec: historic(1).dashboard.spec };
  await act(async () => pending.resolve(restored)); await settle();
  expect(client.getQueryData(["dashboard", "board"])).toEqual(restored);
  expect(client.getQueryState(["dashboards"])?.isInvalidated).toBe(true);
  expect(client.getQueryState(panelKey)?.isInvalidated).toBe(true);
  expect(client.getQueryState(variablesKey)?.isInvalidated).toBe(true);
  expect(api.listVersions.mock.calls.length).toBeGreaterThan(1);
  expect(onRestored).toHaveBeenCalledWith(restored); expect(document.body.textContent).toContain("Restored v1 as v4");
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
  await render(); expect(document.body.textContent).toContain("Loading history");
  await act(async () => pending.resolve([])); await settle(); expect(document.body.textContent).toContain("No versions available");
  await render(false); client.removeQueries({ queryKey: ["dashboard-versions", "board"] });
  api.listVersions.mockRejectedValue(new Error("History failed")); await render(); expect(document.body.textContent).toContain("History failed");
  api.listVersions.mockResolvedValue([]); await act(async () => button("Retry history").click()); await settle(); expect(document.body.textContent).toContain("No versions available");
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
