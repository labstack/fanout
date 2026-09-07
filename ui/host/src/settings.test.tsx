import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Settings from "./settings";

const fetchMock = vi.fn<typeof fetch>();
let role = "admin";

vi.mock("./auth", () => ({
  useViewer: () => ({ id: "u1", email: "v@labstack.com", name: "V", role }),
  authorizedFetch: (url: string, init?: RequestInit) => fetchMock(url, init),
}));

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

const connection = { token_required: true, suggested_endpoint: "ingest.example.com:4317", tls_configured: false, header_name: "Authorization" };

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
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async () => json(connection));
  });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("gives the collector its own endpoint and leaves the token an environment variable", async () => {
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Collector configuration"));
    const config = document.body.textContent ?? "";
    // Quoted, because an advertised IPv6 endpoint is a YAML flow sequence bare.
    expect(config).toContain('endpoint: "ingest.example.com:4317"');
    // A secret pasted into a config file is a secret in version control.
    expect(config).toContain("${env:INGEST_TOKEN}");
    // This instance serves plaintext, so the exporter has to be told.
    expect(config).toContain("insecure: true");
    await act(async () => root.unmount());
  });

  it("asks before rotating, because the current token stops working at once", async () => {
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Rotate token"));
    const rotate = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.trim() === "Rotate token");
    await act(async () => rotate?.click());
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).not.toBeNull());
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("rejected until you update it");
    // Opening the dialog must not have rotated anything.
    expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === "POST")).toBe(false);
    await act(async () => root.unmount());
  });

  it("shows the new token once after rotating", async () => {
    fetchMock.mockImplementation(async (_url, init) => json((init as RequestInit | undefined)?.method === "POST" ? { ...connection, ingest_token: "fo_newtoken" } : connection));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Rotate token"));
    const open = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.trim() === "Rotate token");
    await act(async () => open?.click());
    await vi.waitFor(() => expect(document.querySelector('[role="dialog"]')).not.toBeNull());
    const confirm = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((button) => button.textContent?.trim() === "Rotate token");
    await act(async () => confirm?.click());
    await vi.waitFor(() => expect(document.body.textContent).toContain("fo_newtoken"));
    expect(document.body.textContent).toContain("Fanout shows this once");
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

  it("stops offering to issue a token once one has been issued", async () => {
    fetchMock.mockImplementation(async (_url, init) => json((init as RequestInit | undefined)?.method === "POST"
      ? { ...connection, token_required: true, ingest_token: "fo_firsttoken" }
      : { ...connection, token_required: false }));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("rejecting telemetry"));
    const open = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.trim() === "Issue token");
    await act(async () => open?.click());
    const confirm = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((button) => button.textContent?.trim() === "Issue token");
    await act(async () => confirm?.click());
    await vi.waitFor(() => expect(document.body.textContent).toContain("fo_firsttoken"));
    // The page has changed the fact it describes: leaving the old copy up
    // invites the admin to rotate away the token they were just handed.
    expect(document.body.textContent).not.toContain("rejecting telemetry");
    const done = [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.trim() === "Done");
    await act(async () => done?.click());
    expect(document.body.textContent).not.toContain("fo_firsttoken");
    // "Shown once" has to be true of the cache as well as the screen.
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain("fo_firsttoken");
    expect(document.body.textContent).toContain("Rotate when a token may have been exposed");
    await act(async () => root.unmount());
  });

  it("explains a rotation that failed, and forgets it when the dialog is reopened", async () => {
    fetchMock.mockImplementation(async (_url, init) => ((init as RequestInit | undefined)?.method === "POST" ? json({ message: "nope" }, 500) : json(connection)));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Rotate token"));
    const open = () => [...document.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.trim() === "Rotate token" && !button.closest('[role="dialog"]'));
    await act(async () => open()?.click());
    const confirm = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((button) => button.textContent?.trim() === "Rotate token");
    await act(async () => confirm?.click());
    await vi.waitFor(() => expect(document.body.textContent).toContain("could not issue a new token"));
    const cancel = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((button) => button.textContent?.trim() === "Cancel");
    await act(async () => cancel?.click());
    await act(async () => open()?.click());
    // A stale failure standing over an untouched dialog reads as a new one.
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain("could not issue a new token");
    await act(async () => root.unmount());
  });

  it("offers a retry instead of a spinner when the details cannot be loaded", async () => {
    fetchMock.mockImplementation(async () => json({ message: "boom" }, 500));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("could not load"));
    expect([...document.querySelectorAll("button")].some((button) => button.textContent?.trim() === "Try again")).toBe(true);
    await act(async () => root.unmount());
  });

  it("gives a viewer the connection details without a rotate button", async () => {
    role = "viewer";
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("Collector configuration"));
    expect(document.body.textContent).toContain("An administrator issues and rotates the ingest token");
    expect([...document.querySelectorAll("button")].some((button) => button.textContent?.includes("Rotate token"))).toBe(false);
    await act(async () => root.unmount());
  });

  it("says the workspace is rejecting telemetry when no token exists", async () => {
    fetchMock.mockImplementation(async () => json({ ...connection, token_required: false }));
    const root = await mount();
    await vi.waitFor(() => expect(document.body.textContent).toContain("rejecting telemetry"));
    expect([...document.querySelectorAll("button")].some((button) => button.textContent?.trim() === "Issue token")).toBe(true);
    await act(async () => root.unmount());
  });
});
