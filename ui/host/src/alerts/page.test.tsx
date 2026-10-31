import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Rule } from "./api";

const viewer = vi.hoisted(() => ({ role: "operator" }));
vi.mock("../auth", async original => ({ ...(await original<typeof import("../auth")>()), useViewer: () => ({ id: "u", email: "u@example.test", display_name: "U", status: "active", role: viewer.role }) }));
import { AlertsPage } from "./page";

const rule: Rule = { id: "r1", name: "Checkout errors", enabled: true, service: "checkout", expression: "error_rate > 0.05", for_seconds: 60, cooldown_s: 300, repeat_interval_s: 3600, notify_on_resolve: false, created_at: "", updated_at: "" };
const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => Response.json(body, { status });
const cleanups: (() => void)[] = [];
const calls = (method: string, url: string) => fetchMock.mock.calls.filter(([input, init]) => String(input) === url && (init?.method ?? "GET") === method).map(([, init]) => init?.body ? JSON.parse(String(init.body)) : undefined);
let create: () => Promise<Response>;
let rules: Rule[];
const onExplore = vi.fn();

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  viewer.role = "operator";
  create = async () => json({ ...rule, id: "r2" }, 201);
  rules = [rule];
  onExplore.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input), method = init?.method ?? "GET";
    if (url === "/api/alerting/rules" && method === "GET") return json(rules);
    if (url === "/api/alerting/alerts") return json([{ id: "a1", rule_id: "r1", service: "checkout", state: "firing", value: 0.09, fired_at: "2026-10-30T10:00:00Z", created_at: "2026-10-30T10:00:00Z" }]);
    if (url === "/api/alerting/rules" && method === "POST") return create();
    if (url === "/api/alerting/rules/r1" && method === "PUT") return json({ ...rule, enabled: false });
    if (url === "/api/alerting/rules/r1" && method === "DELETE") return new Response(null, { status: 204 });
    if (url === "/api/alerting/rules/r1/test") return json({ rule, results: [{ service: "checkout", triggered: true, env: {} }] });
    throw new Error(`Unexpected request: ${method} ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render() {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><AlertsPage onExplore={onExplore} /></QueryClientProvider></MantineProvider>));
  cleanups.push(() => { root.unmount(); client.clear(); });
  const ui = within(document.body);
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  // Required fields append an asterisk to their label.
  const input = async (label: string, value: string) => act(async () => { fireEvent.change(ui.getByLabelText(new RegExp(`^${label}`), { selector: "input, textarea" }), { target: { value } }); });
  return { ui, wait, input };
}

describe("AlertsPage", () => {
  it("opens what each rule watches in Explore", async () => {
    rules = [rule, { ...rule, id: "r3", name: "Log flood", service: "", expression: "service == \"error_rate\" && [1].exists(p95x, p95x > 0) && log_count_delta > 50" }, { ...rule, id: "r4", name: "Slow \"api\"", service: "a\"b", expression: "p95 > 500" }];
    const { ui, wait } = await render();
    await wait(() => expect(ui.getAllByRole("button", { name: "Open in Explore" })).toHaveLength(3));
    expect(ui.getByText(/a rule's 0\.05 is 5 there/)).toBeTruthy();
    for (const button of ui.getAllByRole("button", { name: "Open in Explore" })) await act(async () => button.click());
    expect(onExplore.mock.calls.map(([q]) => q)).toEqual([
      'spans where service:"checkout" | error_rate() every auto',
      "logs | count() by service every auto",
      'spans where service:"a""b" | p95(duration) every auto',
    ]);
  });

  it("lists rules with their firing alerts and the history", async () => {
    const { ui, wait } = await render();
    await wait(() => expect(ui.getAllByText("Checkout errors").length).toBeGreaterThan(0));
    expect(ui.getByText("firing · checkout")).toBeTruthy();
    expect(ui.getByText("error_rate > 0.05")).toBeTruthy();
    expect(ui.getByText("0.09")).toBeTruthy();
  });

  it("creates a rule from a template and shows the server's reason when it is rejected", async () => {
    create = async () => json({ code: "invalid_input", message: "invalid expression: undeclared reference to 'errors'" }, 400);
    const { ui, wait, input } = await render();
    await wait(() => expect(ui.getByRole("button", { name: "New rule" })).toBeTruthy());
    await act(async () => ui.getByRole("button", { name: "New rule" }).click());
    await input("Name", "Errors");
    await act(async () => ui.getByRole("button", { name: "Start from a template" }).click());
    await wait(() => expect(ui.getByRole("menuitem", { name: /Error rate/, hidden: true })).toBeTruthy());
    await act(async () => ui.getByRole("menuitem", { name: /Error rate/, hidden: true }).click());
    await act(async () => ui.getByRole("button", { name: "Create rule" }).click());
    await wait(() => expect(ui.getByText(/undeclared reference to 'errors'/)).toBeTruthy());
    expect(calls("POST", "/api/alerting/rules")[0]).toMatchObject({ name: "Errors", expression: "error_rate > 0.05 && throughput > 10", enabled: true, for_seconds: 60 });
  });

  it("rejects webhook headers that are not a JSON object", async () => {
    const { ui, wait, input } = await render();
    await wait(() => expect(ui.getByRole("button", { name: "New rule" })).toBeTruthy());
    await act(async () => ui.getByRole("button", { name: "New rule" }).click());
    await input("Name", "Errors");
    await input("Expression", "p95 > 500");
    await input("Webhook headers", "[1]");
    expect(ui.getByRole<HTMLButtonElement>("button", { name: "Create rule" }).disabled).toBe(true);
    await input("Webhook headers", "{\"X-Team\": \"core\"}");
    expect(ui.getByRole<HTMLButtonElement>("button", { name: "Create rule" }).disabled).toBe(false);
  });

  it("disables a rule by replacing it with every field kept", async () => {
    const { ui, wait } = await render();
    await wait(() => expect(ui.getByRole("switch", { name: "Enable Checkout errors" })).toBeTruthy());
    await act(async () => ui.getByRole("switch", { name: "Enable Checkout errors" }).click());
    await wait(() => expect(calls("PUT", "/api/alerting/rules/r1")).toHaveLength(1));
    const { id: _id, created_at: _c, updated_at: _u, ...fields } = rule;
    expect(calls("PUT", "/api/alerting/rules/r1")[0]).toEqual({ ...fields, enabled: false });
  });

  it("tests and deletes a rule", async () => {
    const { ui, wait } = await render();
    await wait(() => expect(ui.getByRole("button", { name: "Checkout errors actions" })).toBeTruthy());
    await act(async () => ui.getByRole("button", { name: "Checkout errors actions" }).click());
    await act(async () => ui.getByRole("menuitem", { name: "Test against current data", hidden: true }).click());
    await wait(() => expect(ui.getByText("Would fire")).toBeTruthy());
    await act(async () => fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" }));
    await act(async () => ui.getByRole("button", { name: "Checkout errors actions" }).click());
    await act(async () => ui.getByRole("menuitem", { name: "Delete", hidden: true }).click());
    await act(async () => ui.getByRole("button", { name: "Delete rule" }).click());
    await wait(() => expect(calls("DELETE", "/api/alerting/rules/r1")).toHaveLength(1));
  });

  it("shows the last fired time and an unknown state when history cannot load", async () => {
    const { ui, wait } = await render();
    await wait(() => expect(ui.getAllByText(/Oct 30/).length).toBeGreaterThan(1));
    fetchMock.mockImplementation(async (input) => String(input) === "/api/alerting/alerts" ? json({ message: "down" }, 500) : json([rule]));
    cleanups.splice(0).forEach(cleanup => cleanup()); document.body.innerHTML = "";
    const again = await render();
    await again.wait(() => expect(again.ui.getByText("Unknown")).toBeTruthy());
    expect(again.ui.queryByText("OK")).toBeNull();
  });

  it("ignores a test answer that arrives after its window closed", async () => {
    let answer: (response: Response) => void = () => {};
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/alerting/rules/r1/test" ? new Promise<Response>(resolve => { answer = resolve; }) : base(input, init));
    const { ui, wait } = await render();
    await wait(() => expect(ui.getByRole("button", { name: "Checkout errors actions" })).toBeTruthy());
    await act(async () => ui.getByRole("button", { name: "Checkout errors actions" }).click());
    await act(async () => ui.getByRole("menuitem", { name: "Test against current data", hidden: true }).click());
    await wait(() => expect(ui.getByLabelText("Testing the rule")).toBeTruthy());
    await act(async () => ui.getByRole("button", { name: "Close test" }).click());
    await act(async () => answer(json({ rule, results: [{ service: "checkout", triggered: true, env: {} }] })));
    await act(async () => {});
    expect(ui.queryByText("Would fire")).toBeNull();
  });

  it("is read-only for viewers", async () => {
    viewer.role = "viewer";
    const { ui, wait } = await render();
    await wait(() => expect(ui.getByRole("switch", { name: "Enable Checkout errors" })).toBeTruthy());
    expect(ui.queryByRole("button", { name: "New rule" })).toBeNull();
    expect(ui.queryByRole("button", { name: "Checkout errors actions" })).toBeNull();
    expect(ui.getByRole<HTMLInputElement>("switch", { name: "Enable Checkout errors" }).disabled).toBe(true);
  });
});
