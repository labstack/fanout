import { formatTimestamp } from "../../panels/units";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Settings from "./settings";

const app = vi.hoisted(() => ({ agentAvailable: true }));
vi.mock("./app-context", () => ({ useFanoutApp: () => app }));
beforeEach(() => { app.agentAvailable = true; });

const fetchMock = vi.fn<typeof fetch>();
const agentsFetchMock = vi.fn<typeof fetch>();
let role = "admin";
let accountOwnership = "local";

vi.mock("./auth", () => ({
  useRuntimeStatus: () => ({ account_ownership: accountOwnership }),
  useViewer: () => ({ id: "u1", email: "v@labstack.com", display_name: "V", status: "active" as const, role }),
  authorizedFetch: (url: string, init?: RequestInit) => url.startsWith("/api/auth/oauth/connections") ? agentsFetchMock(url, init) : fetchMock(url, init),
}));

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function setValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

const connection = { token_required: true, suggested_endpoint: "ingest.example.com:7520", tls_configured: false, header_name: "Authorization" };
const collectorToken = { id: "token-1", name: "prod-collector", hint: "x7Qa", created_at: "2026-10-01T12:00:00Z", last_used_at: "2026-10-02T12:00:00Z" };
const unusedToken = { id: "token-2", name: "staging", hint: "", created_at: "2026-10-03T12:00:00Z", last_used_at: null };
const tokenList = (...tokens: unknown[]) => ({ tokens, max_active: 50 });
const created = { ...connection, token: { id: "token-3", name: "edge", hint: "AAAA", created_at: "2026-11-01T12:00:00Z", last_used_at: null }, ingest_token: `foi_${"A".repeat(43)}` };
const tokensURL = "/api/settings/ingest/tokens";

function telemetryResponder(tokens: unknown[], post: () => Response) {
  return async (url: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "POST") return post();
    return json(String(url) === tokensURL ? tokenList(...tokens) : connection);
  };
}

function button(label: string) {
  return [...document.querySelectorAll<HTMLButtonElement>("button")].find((candidate) => candidate.textContent?.trim() === label);
}

function postCalls() {
  return fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");
}

async function typeTokenName(value: string) {
  await vi.waitFor(() => expect(document.querySelector<HTMLInputElement>('input[placeholder="production-collector"]')).not.toBeNull());
  const input = document.querySelector<HTMLInputElement>('input[placeholder="production-collector"]');
  if (!input) throw new Error("token name input not found");
  await act(async () => setValue(input, value));
}

async function submitTokenForm() {
  const submit = button("Create token");
  await act(async () => submit?.closest("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
}

let client: QueryClient;

async function mount() {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => root.render(
    <QueryClientProvider client={client}><MantineProvider><Settings /></MantineProvider></QueryClientProvider>,
  ));
  return root;
}

describe("connect telemetry", () => {
  beforeEach(() => {
    role = "admin";
    accountOwnership = "local";
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    agentsFetchMock.mockReset();
    agentsFetchMock.mockImplementation(async () => json({ connections: [] }));
    fetchMock.mockImplementation(telemetryResponder([collectorToken], () => json(created, 201)));
  });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("says nothing about account ownership when accounts are managed locally", async () => {
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Connect telemetry"));
    expect(document.body.textContent).not.toContain("User accounts");
    await act(async () => root.unmount());
  });

  it.each([
    ["identity_provider_roles", "Managed by your identity provider", "creates accounts at first sign-in and assigns roles", "suspend or reactivate"],
    ["identity_provider_roles_no_provisioning", "New people cannot be added", "does not create accounts at sign-in", "automatic provisioning with an allow-list or SCIM"],
    ["identity_provider_directory", "Managed by your identity provider", "owns accounts through SCIM", "status, or email"],
  ])("explains what the server reports when ownership is %s", async (ownership, title, statement, detail) => {
    accountOwnership = ownership;
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain(title));
    expect(document.body.textContent).toContain(statement);
    expect(document.body.textContent).toContain(detail);
    await act(async () => root.unmount());
  });

  it.each([false, true])("shows chat setup guidance only when agent_available=%s is false", async (available) => {
    app.agentAvailable = available;
    const root = await mount();
    try {
      await vi.waitFor(() => expect(document.body.textContent).toContain("Connected agents"));
      expect(document.body.textContent?.includes("Chat is disabled")).toBe(!available);
      expect(document.body.textContent?.includes("FANOUT_AI_PROVIDER")).toBe(!available);
      if (!available) {
        expect(document.body.textContent).toContain("anthropic");
        expect(document.body.textContent).toContain("ANTHROPIC_API_KEY");
        expect(document.body.textContent).toContain("openai");
        expect(document.body.textContent).toContain("OPENAI_API_KEY");
        expect(document.body.textContent).toContain("Claude, Codex, or ChatGPT");
        expect(document.querySelector<HTMLAnchorElement>('a[href="https://fanout.run/docs/guides/mcp"]')).not.toBeNull();
      }
    } finally { await act(async () => root.unmount()); }
  });

  it("gives the collector its own endpoint and leaves the token an environment variable", async () => {
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Collector configuration"));
    const config = document.body.textContent ?? "";
    // Quoted, because an advertised IPv6 endpoint is a YAML flow sequence bare.
    expect(config).toContain('endpoint: "ingest.example.com:7520"');
    // A secret pasted into a config file is a secret in version control.
    expect(config).toContain("${env:INGEST_TOKEN}");
    // This instance serves plaintext, so the exporter has to be told.
    expect(config).toContain("insecure: true");
    await act(async () => root.unmount());
  });

  it("lets the operator use a different collector route without server configuration", async () => {
    fetchMock.mockImplementation(async () => json({ ...connection, suggested_endpoint: "https://fanout.example.com" }));
    const root = await mount();
    await vi.waitFor(() => expect(document.querySelector<HTMLInputElement>('input[aria-label="Endpoint"]')?.value).toBe("https://fanout.example.com"));
    const endpoint = document.querySelector<HTMLInputElement>('input[aria-label="Endpoint"]');
    if (!endpoint) throw new Error("endpoint input not found");
    await act(async () => setValue(endpoint, "https://fanout.internal.example.com"));
    expect(document.body.textContent).toContain('endpoint: "https://fanout.internal.example.com:443"');
    expect(document.body.textContent).not.toContain('endpoint: "https://fanout.example.com:443"');
    await act(async () => root.unmount());
  });

  it("keeps generated configuration valid while the endpoint field is empty", async () => {
    fetchMock.mockImplementation(async () => json({ ...connection, suggested_endpoint: "https://fanout.example.com" }));
    const root = await mount();
    await vi.waitFor(() => expect(document.querySelector<HTMLInputElement>('input[aria-label="Endpoint"]')).not.toBeNull());
    const endpoint = document.querySelector<HTMLInputElement>('input[aria-label="Endpoint"]');
    if (!endpoint) throw new Error("endpoint input not found");
    await act(async () => setValue(endpoint, "   "));
    expect(endpoint.value).toBe("   ");
    expect(document.body.textContent).toContain('endpoint: "https://fanout.example.com:443"');
    await act(async () => root.unmount());
  });

  it("lists tokens with name, hint, created time, and last used", async () => {
    fetchMock.mockImplementation(telemetryResponder([collectorToken, unusedToken], () => json(created, 201)));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("prod-collector"));
    expect(document.body.textContent).toContain("foi_…x7Qa");
    expect(document.body.textContent).toContain(formatTimestamp(Date.parse(collectorToken.created_at)));
    expect(document.body.textContent).toContain(formatTimestamp(Date.parse(collectorToken.last_used_at)));
    // A migrated token has no recorded hint, and an unused one has no last use.
    expect(document.body.textContent).toContain("Not recorded");
    expect(document.body.textContent).toContain("Never");
    await act(async () => root.unmount());
  });

  it("creates a token only on an explicit submit, never on page load", async () => {
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("prod-collector"));
    expect(postCalls()).toHaveLength(0);
    await typeTokenName("edge");
    expect(postCalls()).toHaveLength(0);
    await submitTokenForm();
    await vi.waitFor(() => expect(postCalls()).toHaveLength(1));
    const [url, init] = postCalls()[0];
    expect(url).toBe(tokensURL);
    expect(init?.body).toBe(JSON.stringify({ name: "edge" }));
    await act(async () => root.unmount());
  });

  it("shows the value once with a warning and the Collector configuration", async () => {
    const root = await mount();
    await typeTokenName("edge");
    await submitTokenForm();
    const value = `foi_${"A".repeat(43)}`;
    await vi.waitFor(() => expect(document.body.textContent).toContain(`INGEST_TOKEN=${value}`));
    expect(document.body.textContent).toContain("Save this token now");
    expect(document.body.textContent).toContain("${env:INGEST_TOKEN}");
    await act(async () => button("Done")?.click());
    expect(document.body.textContent).not.toContain(value);
    // "Shown once" has to be true of the cache as well as the screen.
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(value);
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(value);
    await act(async () => root.unmount());
  });

  it("reports a refused create with the server's reason", async () => {
    fetchMock.mockImplementation(telemetryResponder([collectorToken], () => json({ code: "ingest_token_name_taken", message: "An active token already uses that name. Choose another name." }, 409)));
    const root = await mount();
    await typeTokenName("prod-collector");
    await submitTokenForm();
    await vi.waitFor(() => expect(document.body.textContent).toContain("already uses that name"));
    await act(async () => root.unmount());
  });

  it("stops offering create at the active token limit", async () => {
    const full = Array.from({ length: 50 }, (_, index) => ({ ...collectorToken, id: `token-${index}`, name: `collector-${index}` }));
    fetchMock.mockImplementation(telemetryResponder(full, () => json(created, 201)));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("collector-0"));
    await typeTokenName("one-more");
    expect(button("Create token")?.disabled).toBe(true);
    expect(document.body.textContent).toContain("50 active tokens, the maximum");
    await act(async () => root.unmount());
  });

  it("warns that revoking the last active token stops ingest", async () => {
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("prod-collector"));
    await act(async () => button("Revoke")?.click());
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).not.toBeNull());
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain("last active token");
    expect(dialog?.textContent).toContain("stops all ingest");
    // Opening the dialog must not have revoked anything.
    expect(postCalls()).toHaveLength(0);
    await act(async () => root.unmount());
  });

  it("omits the last-token warning while other tokens remain", async () => {
    fetchMock.mockImplementation(telemetryResponder([collectorToken, unusedToken], () => json(created, 201)));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("prod-collector"));
    await act(async () => button("Revoke")?.click());
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).not.toBeNull());
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain("last active token");
    await act(async () => root.unmount());
  });

  it("revokes through the token route and refreshes the list", async () => {
    let tokens: unknown[] = [collectorToken, unusedToken];
    fetchMock.mockImplementation(async (url, init) => {
      if (init?.method === "POST") { tokens = [unusedToken]; return new Response(null, { status: 204 }); }
      return json(String(url) === tokensURL ? tokenList(...tokens) : connection);
    });
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("prod-collector"));
    await act(async () => button("Revoke")?.click());
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).not.toBeNull());
    const confirm = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((candidate) => candidate.textContent?.trim() === "Revoke token");
    await act(async () => confirm?.click());
    await vi.waitFor(() => expect(document.body.textContent).not.toContain("prod-collector"));
    expect(postCalls()[0][0]).toBe(`${tokensURL}/token-1/revoke`);
    await act(async () => root.unmount());
  });

  it("omits tls.insecure when the advertised endpoint is already https", async () => {
    fetchMock.mockImplementation(async () => json({ ...connection, suggested_endpoint: "https://ingest.example.com" }));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Collector configuration"));
    expect(document.body.textContent).not.toContain("insecure: true");
    expect(document.body.textContent).toContain("secured with TLS");
    await act(async () => root.unmount());
  });

  it.each([
    ["https://ingest.example.com", "https://ingest.example.com:443", false],
    ["http://ingest.example.com", "http://ingest.example.com:80", true],
    ["https://ingest.example.com:443/", "https://ingest.example.com:443", false],
    ["https://ingest.example.com:7520", "https://ingest.example.com:7520", false],
    ["http://localhost:7520", "http://localhost:7520", true],
    ["https://[2001:db8::1]/", "https://[2001:db8::1]:443", false],
    ["http://[::1]:7520", "http://[::1]:7520", true],
    ["[::1]:7520", "[::1]:7520", true],
  ])("generates a Collector endpoint with an explicit port for %s", async (endpoint, expected, insecure) => {
    fetchMock.mockImplementation(async () => json({ ...connection, suggested_endpoint: endpoint }));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Collector configuration"));
    const config = document.body.textContent ?? "";
    expect(config).toContain(`endpoint: "${expected}"`);
    expect(config.includes("insecure: true")).toBe(insecure);
    await act(async () => root.unmount());
  });

  it("treats an endpoint on port 443 as TLS even without a scheme", async () => {
    // An operator naming a TLS-terminating proxy usually writes the port and
    // not the scheme, and tls.insecure against it fails with a gRPC error that
    // explains nothing.
    fetchMock.mockImplementation(async () => json({ ...connection, suggested_endpoint: "ingest.example.com:443" }));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Collector configuration"));
    expect(document.body.textContent).not.toContain("insecure: true");
    await act(async () => root.unmount());
  });

  it("offers a retry instead of a spinner when the details cannot be loaded", async () => {
    fetchMock.mockImplementation(async () => json({ message: "boom" }, 500));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("could not load"));
    expect([...document.querySelectorAll("button")].some((button) => button.textContent?.trim() === "Try again")).toBe(true);
    await act(async () => root.unmount());
  });

  it("gives a viewer the connection details without token management or token details", async () => {
    role = "viewer";
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Collector configuration"));
    expect(document.body.textContent).toContain("An administrator creates and revokes ingest tokens");
    expect(button("Create token")).toBeUndefined();
    expect(button("Revoke")).toBeUndefined();
    expect(fetchMock.mock.calls.some(([url]) => String(url) === tokensURL)).toBe(false);
    expect(document.body.textContent).not.toContain("prod-collector");
    await act(async () => root.unmount());
  });

  it("says the workspace is rejecting telemetry when no token is active", async () => {
    fetchMock.mockImplementation(async (url) => json(String(url) === tokensURL ? tokenList() : { ...connection, token_required: false }));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("rejecting telemetry"));
    expect(button("Create token")).not.toBeUndefined();
    await act(async () => root.unmount());
  });
});


describe("connected agents", () => {
  const codex = { client_id: "codex-id", client_name: "Codex", connected_at: "2026-10-01T12:00:00Z", refreshed_at: "2026-10-02T12:00:00Z", scope: "telemetry:read" };
  const claude = { ...codex, client_id: "claude-id", client_name: "Claude Code" };

  beforeEach(() => {
    role = "viewer";
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    agentsFetchMock.mockReset();
    fetchMock.mockImplementation(async () => json(connection));
    agentsFetchMock.mockImplementation(async () => json({ connections: [codex, claude] }));
  });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  function disconnectButton(scope: ParentNode = document) {
    const button = [...scope.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.trim() === (scope === document ? "Disconnect" : "Disconnect agent"));
    if (!button) throw new Error("disconnect button not found");
    return button;
  }

  async function openDisconnect() {
    await vi.waitFor(() => expect(document.body.textContent).toContain("Claude Code"));
    await act(async () => disconnectButton().click());
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).not.toBeNull());
    const dialog = document.querySelector('[role="dialog"]');
    if (!dialog) throw new Error("confirmation dialog not found");
    return dialog;
  }

  it("lists names and connection dates for a viewer", async () => {
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Claude Code"));
    expect(document.body.textContent).toContain("Connected agents");
    expect(document.body.textContent).toContain("Codex");
    expect(document.body.textContent).toContain(`Connected${formatTimestamp(Date.parse(codex.connected_at))}`);
    expect(document.querySelector("time")?.getAttribute("datetime")).toBe(codex.connected_at);
    expect(document.querySelectorAll("time")).toHaveLength(2);
    await act(async () => root.unmount());
  });

  it("links the empty state to the MCP connect guide", async () => {
    agentsFetchMock.mockImplementation(async () => json({ connections: [] }));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("No agents connected"));
    expect(document.querySelector<HTMLAnchorElement>('a[href="https://fanout.run/docs/guides/mcp"]')?.textContent).toBe("Connect an agent over MCP");
    await act(async () => root.unmount());
  });

  it("confirms before disconnecting and allows cancellation", async () => {
    const root = await mount();
    const dialog = await openDisconnect();
    expect(dialog.textContent).toContain("Disconnect Codex?");
    expect(agentsFetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    const cancel = [...dialog.querySelectorAll("button")].find((button) => button.textContent?.trim() === "Cancel");
    if (!cancel) throw new Error("cancel button not found");
    await act(async () => cancel.click());
    expect(agentsFetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(false);
    expect(document.querySelectorAll("time")).toHaveLength(2);
    await act(async () => root.unmount());
  });

  it("removes only the disconnected row and announces the result", async () => {
    agentsFetchMock.mockImplementation(async (_url, init) => json(init?.method === "POST" ? { ok: true } : { connections: [codex, claude] }));
    const root = await mount();
    const dialog = await openDisconnect();
    await act(async () => disconnectButton(dialog).click());
    await vi.waitFor(() => expect([...document.querySelectorAll('[role="status"]')].some((status) => status.textContent === "Codex disconnected.")).toBe(true));
    expect(agentsFetchMock).toHaveBeenCalledWith("/api/auth/oauth/connections/codex-id/revoke", { method: "POST" });
    expect(document.querySelectorAll("time")).toHaveLength(1);
    expect(document.body.textContent).toContain("Claude Code");
    const cached = client.getQueryData<{ connections: typeof codex[] }>(["settings", "oauth", "connections"]);
    expect(cached?.connections.map((agent) => agent.client_id)).toEqual(["claude-id"]);
    await act(async () => root.unmount());
  });

  it("keeps the row and shows a failed disconnect so it can be retried", async () => {
    agentsFetchMock.mockImplementation(async (_url, init) => init?.method === "POST" ? json({ code: "internal", message: "failed" }, 500) : json({ connections: [codex, claude] }));
    const root = await mount();
    const dialog = await openDisconnect();
    await act(async () => disconnectButton(dialog).click());
    await vi.waitFor(() => expect(dialog.textContent).toContain("could not disconnect this agent"));
    expect(document.querySelectorAll("time")).toHaveLength(2);
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("Try again");
    expect(document.body.textContent).not.toContain("Codex disconnected.");
    agentsFetchMock.mockImplementation(async () => json({ ok: true }));
    await act(async () => disconnectButton(dialog).click());
    await vi.waitFor(() => expect(document.querySelectorAll("time")).toHaveLength(1));
    await act(async () => root.unmount());
  });

  it("shows a network failure without removing the agent", async () => {
    agentsFetchMock.mockImplementation(async (_url, init) => {
      if (init?.method === "POST") throw new Error("Failed to fetch");
      return json({ connections: [codex, claude] });
    });
    const root = await mount();
    const dialog = await openDisconnect();
    await act(async () => disconnectButton(dialog).click());
    await vi.waitFor(() => expect(dialog.textContent).toContain("could not disconnect this agent"));
    expect(document.querySelectorAll("time")).toHaveLength(2);
    await act(async () => root.unmount());
  });

  it("offers a retry when agents cannot be loaded", async () => {
    agentsFetchMock.mockImplementation(async () => json({ code: "internal", message: "failed" }, 500));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("could not load your connected agents"));
    expect(document.body.textContent).not.toContain("No agents connected");
    agentsFetchMock.mockImplementation(async () => json({ connections: [codex] }));
    const retry = [...document.querySelectorAll("button")].find((button) => button.textContent?.trim() === "Try again");
    if (!retry) throw new Error("retry button not found");
    await act(async () => retry.click());
    await vi.waitFor(() => expect(document.querySelectorAll("time")).toHaveLength(1));
    await act(async () => root.unmount());
  });
});
