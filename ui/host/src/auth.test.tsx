import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AuthGate from "./auth";

declare global {
  interface Window { happyDOM: { setURL(url: string): void } }
}

const fetchMock = vi.fn<typeof fetch>();
const returnTo = "/api/auth/oauth/authorize?client_id=test-client&state=test-state";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function authResponse(input: RequestInfo | URL, user: unknown) {
  const path = String(input);
  if (path === "/api/auth/status") {
    return json({ setup_required: false, public_read: true, auth_mode: "local" });
  }
  if (path === "/api/auth/me") return json(user);
  throw new Error(`unexpected request: ${path}`);
}

describe("AuthGate OAuth return", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    window.happyDOM.setURL(`https://fanout.example.com/?return_to=${encodeURIComponent(returnTo)}`);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
    window.happyDOM.setURL("https://fanout.example.com/");
  });

  it("shows login instead of redirecting the anonymous public viewer", async () => {
    fetchMock.mockImplementation(async (input) => authResponse(input, {
      id: "synthetic-viewer",
      role: "viewer",
      anonymous: true,
    }));
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(
      <MantineProvider>
        <AuthGate><div>Fanout application</div></AuthGate>
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Sign in to investigate"));
    expect(window.location.pathname).toBe("/");
    expect(window.location.search).toContain("return_to=");
    expect(document.body.textContent).not.toContain("Fanout application");

    await act(async () => root.unmount());
  });

  it("redirects a persisted user to OAuth authorization", async () => {
    fetchMock.mockImplementation(async (input) => authResponse(input, {
      id: "user-123",
      role: "admin",
      anonymous: false,
    }));
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(
      <MantineProvider>
        <AuthGate><div>Fanout application</div></AuthGate>
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(window.location.pathname).toBe("/api/auth/oauth/authorize"));
    expect(window.location.search).toBe("?client_id=test-client&state=test-state");

    await act(async () => root.unmount());
  });

  it("renders the app for the anonymous viewer outside the OAuth flow", async () => {
    window.happyDOM.setURL("https://fanout.example.com/");
    fetchMock.mockImplementation(async (input) => authResponse(input, {
      id: "synthetic-viewer",
      role: "viewer",
      anonymous: true,
    }));
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(
      <MantineProvider>
        <AuthGate><div>Fanout application</div></AuthGate>
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Fanout application"));
    expect(document.body.textContent).not.toContain("Sign in to investigate");

    await act(async () => root.unmount());
  });
});
