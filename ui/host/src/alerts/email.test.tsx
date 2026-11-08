import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EmailOptions, Rule } from "./api";

const viewer = vi.hoisted(() => ({ role: "operator" }));
vi.mock("../auth", async original => ({ ...(await original<typeof import("../auth")>()), useViewer: () => ({ id: "u", email: "u@example.test", display_name: "U", status: "active", role: viewer.role }) }));
import { AlertsPage } from "./page";

const rule: Rule = { id: "r1", name: "Checkout errors", enabled: true, service: "checkout", expression: "error_rate > 0.05", for_seconds: 60, cooldown_s: 300, repeat_interval_s: 3600, notify_on_resolve: false, created_at: "", updated_at: "" };
const available: EmailOptions = { available: true, default_destination: true, max_recipients: 3, hourly_limit: 30 };
const smtpMessage = "Email delivery is unavailable because SMTP is not configured.";
const unavailable: EmailOptions = { available: false, message: smtpMessage, default_destination: false, max_recipients: 3 };
const fetchMock = vi.fn<typeof fetch>();
const cleanups: (() => void)[] = [];
let rules: Rule[];
let options: EmailOptions;
const posted = () => fetchMock.mock.calls.filter(([url, init]) => String(url) === "/api/alerting/rules" && init?.method === "POST").map(([, init]) => JSON.parse(String(init?.body)));

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  viewer.role = "operator";
  rules = [rule];
  options = available;
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input), method = init?.method ?? "GET";
    if (url === "/api/alerting/rules" && method === "GET") return Response.json({ rules });
    if (url === "/api/alerting/alerts") return Response.json({ alerts: [] });
    if (url === "/api/alerting/email") return Response.json(options);
    if (url === "/api/alerting/rules" && method === "POST") return Response.json({ ...rule, id: "r2" }, { status: 201 });
    if (url === "/api/alerting/rules/r1" && method === "PUT") return Response.json(rule);
    if (url === "/api/telemetry/schema?window=1h") return Response.json({ services: [] });
    throw new Error(`Unexpected request: ${method} ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render() {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><AlertsPage onExplore={vi.fn()} /></QueryClientProvider></MantineProvider>));
  cleanups.push(() => { root.unmount(); client.clear(); });
  const ui = within(document.body);
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  const input = async (label: RegExp, value: string) => act(async () => { fireEvent.change(ui.getByLabelText(label, { selector: "input, textarea" }), { target: { value } }); });
  const openNew = async () => { await wait(() => ui.getByRole("button", { name: "New rule" })); await act(async () => { fireEvent.click(ui.getAllByRole("button", { name: "New rule" })[0]); }); await wait(() => ui.getByLabelText(/^Email recipients/, { selector: "input" })); };
  return { ui, wait, input, openNew };
}

const field = (ui: ReturnType<typeof within>) => ui.getByLabelText(/^Email recipients/, { selector: "input" }) as HTMLInputElement;

describe("alert rule email", () => {
  it("sends the addresses entered on a new rule", async () => {
    const { ui, wait, input, openNew } = await render();
    await openNew();
    await wait(() => expect(field(ui).disabled).toBe(false));
    await input(/^Name/, "Errors");
    await input(/^Expression/, "error_rate > 0.05");
    await input(/^Email recipients/, "a@example.com, b@example.com");
    await act(async () => { fireEvent.click(ui.getByRole("button", { name: "Create rule" })); });
    await wait(() => expect(posted()[0]?.email_recipients).toEqual(["a@example.com", "b@example.com"]));
  });

  it("explains the default destination when one is configured", async () => {
    const { ui, wait, openNew } = await render();
    await openNew();
    await wait(() => ui.getByText(/use the default destination/));
  });

  it.each([["not-an-address", /is not an email address/], ["a@e.co b@e.co c@e.co d@e.co", /at most 3/]])("blocks saving %s", async (value, message) => {
    const { ui, wait, input, openNew } = await render();
    await openNew();
    await wait(() => expect(field(ui).disabled).toBe(false));
    await input(/^Name/, "Errors");
    await input(/^Expression/, "true");
    await input(/^Email recipients/, value);
    await wait(() => ui.getByText(message));
    expect((ui.getByRole("button", { name: "Create rule" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("disables the option and says why without SMTP", async () => {
    options = unavailable;
    const { ui, wait, openNew } = await render();
    await openNew();
    await wait(() => ui.getByText(smtpMessage));
    expect(field(ui).disabled).toBe(true);
  });

  it("keeps a saved rule editable without SMTP so its recipients can be removed", async () => {
    options = unavailable;
    rules = [{ ...rule, email_recipients: ["a@example.com"], email_status: "unavailable", email_status_detail: smtpMessage }];
    const { ui, wait } = await render();
    await wait(() => ui.getByRole("button", { name: "Checkout errors actions" }));
    await act(async () => { fireEvent.click(ui.getByRole("button", { name: "Checkout errors actions" })); });
    await wait(() => ui.getByRole("menuitem", { name: "Edit" }));
    await act(async () => { fireEvent.click(ui.getByRole("menuitem", { name: "Edit" })); });
    await wait(() => expect(field(ui).value).toBe("a@example.com"));
    expect(field(ui).disabled).toBe(false);
  });

  it.each([
    ["failed", "Email failed", "1 of 1 recipients failed: 550 no such user"],
    ["unavailable", "Email unavailable", smtpMessage],
    ["suppressed", "Email suppressed", "1 of 1 recipients suppressed: Over the limit of 30 emails per hour to this address"],
    ["retrying", "Email retrying", "Attempt 1 of 5 failed: 451"],
  ])("shows a %s email on the rule", async (status, label, detail) => {
    rules = [{ ...rule, email_recipients: ["a@example.com"], email_status: status, email_status_detail: detail }];
    const { ui, wait } = await render();
    await wait(() => ui.getByText(label));
    ui.getByText(detail);
  });

  it("renders the server's reduced rule for a read-only user", async () => {
    viewer.role = "viewer";
    rules = [{ ...rule, email_status: "failed" }];
    const { ui, wait } = await render();
    await wait(() => ui.getByText("Email failed"));
    ui.getByText("Checkout errors");
    ui.getByText("error_rate > 0.05");
    expect(ui.queryByRole("button", { name: "Checkout errors actions" })).toBeNull();
    expect(ui.queryByRole("button", { name: "New rule" })).toBeNull();
    expect(ui.getByRole<HTMLInputElement>("switch", { name: "Enable Checkout errors" }).disabled).toBe(true);
    expect(fetchMock.mock.calls.some(([, init]) => (init?.method ?? "GET") !== "GET")).toBe(false);
  });

  it("shows nothing on the rule when email was sent", async () => {
    rules = [{ ...rule, email_recipients: ["a@example.com"], email_status: "sent" }];
    const { ui, wait } = await render();
    await wait(() => ui.getByText("Checkout errors"));
    expect(ui.queryByText(/^Email /)).toBeNull();
  });

  it("does not send server-owned status back on save", async () => {
    rules = [{ ...rule, email_recipients: ["a@example.com"], email_status: "failed", email_status_detail: "x", email_status_at: "t" }];
    const { ui, wait } = await render();
    await wait(() => ui.getByRole("switch", { name: "Enable Checkout errors" }));
    await act(async () => { fireEvent.click(ui.getByRole("switch", { name: "Enable Checkout errors" })); });
    await wait(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    const body = JSON.parse(String(fetchMock.mock.calls.find(([, init]) => init?.method === "PUT")?.[1]?.body));
    expect(body.email_recipients).toEqual(["a@example.com"]);
    expect(body).not.toHaveProperty("email_status");
    expect(body).not.toHaveProperty("email_status_detail");
    expect(body).not.toHaveProperty("email_status_at");
  });
});
