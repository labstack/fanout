import { MantineProvider } from "@mantine/core";
import { createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
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

function authResponse(input: RequestInfo | URL, user: unknown, userStatus = 200) {
  const path = String(input);
  if (path === "/api/auth/status") {
    return json({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true });
  }
  if (path === "/api/auth/me") return json(user, userStatus);
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

  it("shows login instead of redirecting without an account session", async () => {
    fetchMock.mockImplementation(async (input) => authResponse(input, { message: "not authenticated" }, 401));
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

  it("renders the app for a persisted viewer outside the OAuth flow", async () => {
    window.happyDOM.setURL("https://fanout.example.com/");
    fetchMock.mockImplementation(async (input) => authResponse(input, {
      id: "viewer-123",
      role: "viewer",
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

  it("shows a recoverable error when runtime status cannot be loaded", async () => {
    window.happyDOM.setURL("https://fanout.example.com/");
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === "/api/auth/status") return json({ message: "status unavailable" }, 500);
      if (String(input) === "/api/auth/me") return json({ message: "not authenticated" }, 401);
      throw new Error(`unexpected request: ${String(input)}`);
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(
      <MantineProvider>
        <AuthGate><div>Fanout application</div></AuthGate>
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Fanout is unavailable"));
    expect(document.body.textContent).toContain("status unavailable");
    await act(async () => root.unmount());
  });

  it("redeems a login link once and removes the credential from the URL", async () => {
    window.happyDOM.setURL("https://fanout.example.com/login?login_token=one-time-secret");
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === "/api/auth/status") return json({ setup_required: false, auth_mode: "local", agent_available: false, smtp_configured: false });
      if (path === "/api/auth/me") return json({ message: "not authenticated" }, 401);
      if (path === "/api/auth/login-link" && init?.method === "POST") {
        expect(JSON.parse(String(init.body))).toEqual({ token: "one-time-secret" });
        return json({ status: "authenticated" });
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const rootRoute = createRootRoute({
      component: () => <AuthGate><div>Fanout application</div></AuthGate>,
    });
    const loginRoute = createRoute({
      getParentRoute: () => rootRoute,
      path: "/login",
      component: () => null,
    });
    const testRouter = createRouter({ routeTree: rootRoute.addChildren([loginRoute]) });

    await act(async () => root.render(
      <MantineProvider>
        <RouterProvider router={testRouter} />
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Fanout application"));
    expect(window.location.search).toBe("");
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/auth/login-link")).toHaveLength(1);

    await act(async () => root.unmount());
  });
});
