import { MantineProvider } from "@mantine/core";
import { createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AuthGate, { useViewer } from "./auth";

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
    return json({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: false });
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
    vi.useRealTimers();
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

    await vi.waitFor(() => expect(document.body.textContent).toContain("Sign in"));
    expect(document.body.textContent).not.toContain("Sign in to investigate");
    expect(window.location.pathname).toBe("/");
    expect(window.location.search).toContain("return_to=");
    expect(document.body.textContent).not.toContain("Fanout application");

    await act(async () => root.unmount());
  });

  it("explains viewer account creation when local self-signup is enabled", async () => {
    window.happyDOM.setURL("https://fanout.example.com/");
    fetchMock.mockImplementation(async (input) => {
      const path = String(input);
      if (path === "/api/auth/status") return json({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: true });
      if (path === "/api/auth/me") return json({ message: "not authenticated" }, 401);
      throw new Error(`unexpected request: ${path}`);
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(
      <MantineProvider>
        <AuthGate><div>Fanout application</div></AuthGate>
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Sign in or create an account"));
    expect(document.body.textContent).toContain("create a viewer account");
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
    expect(document.body.textContent).not.toContain("Sign in");

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
      if (path === "/api/auth/status") return json({ setup_required: false, auth_mode: "local", agent_available: false, smtp_configured: false, self_signup: false });
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

  it("exposes the signed-in account through useViewer", async () => {
    window.happyDOM.setURL("https://fanout.example.com/");
    fetchMock.mockImplementation(async (input) => authResponse(input, {
      id: "viewer-123",
      email: "v@example.com",
      name: "Vee",
      role: "viewer",
    }));
    function Probe() {
      const viewer = useViewer();
      return <div>Signed in as {viewer.email} ({viewer.role})</div>;
    }
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(
      <MantineProvider>
        <AuthGate><Probe /></AuthGate>
      </MantineProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Signed in as v@example.com (viewer)"));
    await act(async () => root.unmount());
  });

  it("verifies automatically after six digits and offers resend and change email", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "setInterval", "clearTimeout", "clearInterval", "Date"] });
    window.happyDOM.setURL("https://fanout.example.com/");
    const posts: Array<{ path: string; body: unknown }> = [];
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === "/api/auth/status") return json({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: false });
      if (path === "/api/auth/me") return json({ message: "not authenticated" }, 401);
      if (init?.method === "POST") {
        posts.push({ path, body: JSON.parse(String(init.body)) });
        if (path === "/api/auth/start") return json({ code_sent: true });
        if (path === "/api/auth/verify") return json({ status: "authenticated" });
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><AuthGate><div>Fanout application</div></AuthGate></MantineProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Sign in"));
    expect(document.body.textContent).not.toContain("Sign in to investigate");

    const email = document.querySelector('input[type="email"]') as HTMLInputElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(email, "v@example.com");
      email.dispatchEvent(new InputEvent("input", { bubbles: true, data: "v@example.com", inputType: "insertText" }));
    });
    const send = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Send code"));
    await act(async () => send?.click());
    await vi.waitFor(() => expect(posts.map((post) => post.path)).toEqual(["/api/auth/start"]));
    expect(document.body.textContent).toContain("Check your email");
    expect(document.body.textContent).toContain("We sent a code to v@example.com");
    expect(document.body.textContent).toContain("Sent to v@example.com");
    expect(document.body.textContent).toContain("Codes expire in 5 minutes");

    const resend = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Resend"));
    expect(resend?.disabled).toBe(true);
    expect(resend?.textContent).toMatch(/Resend in \d+s/);
    await act(async () => { vi.advanceTimersByTime(31_000); });
    await vi.waitFor(() => expect(Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Resend"))?.disabled).toBe(false));
    const armed = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Resend"));
    expect(armed?.textContent).toBe("Resend code");
    await act(async () => { vi.advanceTimersByTime(5_000); });
    expect(Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Resend"))?.textContent).toBe("Resend code");
    expect(vi.getTimerCount()).toBe(0);

    const change = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Change email"));
    await act(async () => change?.click());
    expect((document.querySelector('input[type="email"]') as HTMLInputElement).disabled).toBe(false);
    expect(document.querySelectorAll('input[inputmode="numeric"]')).toHaveLength(0);

    const resend2 = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Send code"));
    await act(async () => resend2?.click());
    await vi.waitFor(() => expect(posts.map((post) => post.path)).toEqual(["/api/auth/start", "/api/auth/start"]));

    const cells = Array.from(document.querySelectorAll<HTMLInputElement>('[data-pin-input] input, input[inputmode="numeric"]'));
    expect(cells).toHaveLength(6);
    for (const [index, digit] of ["1", "2", "3", "4", "5", "6"].entries()) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(cells[index], digit);
        cells[index].dispatchEvent(new InputEvent("input", { bubbles: true, data: digit, inputType: "insertText" }));
      });
    }
    await vi.waitFor(() => expect(posts.at(-1)).toEqual({ path: "/api/auth/verify", body: { email: "v@example.com", code: "123456" } }));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Fanout application"));

    const settled = posts.length;
    await act(async () => { vi.advanceTimersByTime(5_000); });
    expect(posts).toHaveLength(settled);
    expect(posts.filter((post) => post.path === "/api/auth/start")).toHaveLength(2);
    expect(posts.filter((post) => post.path === "/api/auth/verify")).toHaveLength(1);
    expect(vi.getTimerCount()).toBe(0);

    vi.useRealTimers();
    await act(async () => root.unmount());
  });

  it("offers a verify button and returns focus to the first digit after a rejected code", async () => {
    window.happyDOM.setURL("https://fanout.example.com/");
    const posts: Array<{ path: string; body: unknown }> = [];
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path === "/api/auth/status") return json({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: false });
      if (path === "/api/auth/me") return json({ message: "not authenticated" }, 401);
      if (init?.method === "POST") {
        posts.push({ path, body: JSON.parse(String(init.body)) });
        if (path === "/api/auth/start") return json({ code_sent: true });
        if (path === "/api/auth/verify") return json({ message: "invalid or expired code" }, 401);
      }
      throw new Error(`unexpected request: ${path}`);
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><AuthGate><div>Fanout application</div></AuthGate></MantineProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Sign in"));

    const email = document.querySelector('input[type="email"]') as HTMLInputElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(email, "v@example.com");
      email.dispatchEvent(new InputEvent("input", { bubbles: true, data: "v@example.com", inputType: "insertText" }));
    });
    const send = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Send code"));
    await act(async () => send?.click());
    await vi.waitFor(() => expect(document.body.textContent).toContain("Check your email"));

    const verify = () => Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Verify code"));
    expect(verify()).toBeDefined();
    expect(verify()?.disabled).toBe(true);

    const cells = Array.from(document.querySelectorAll<HTMLInputElement>('input[inputmode="numeric"]'));
    expect(cells).toHaveLength(6);
    for (const [index, digit] of ["1", "2", "3", "4", "5", "6"].entries()) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(cells[index], digit);
        cells[index].dispatchEvent(new InputEvent("input", { bubbles: true, data: digit, inputType: "insertText" }));
      });
    }

    await vi.waitFor(() => expect(document.body.textContent).toContain("invalid or expired code"));
    const cleared = Array.from(document.querySelectorAll<HTMLInputElement>('input[inputmode="numeric"]'));
    expect(cleared.map((cell) => cell.value)).toEqual(["", "", "", "", "", ""]);
    await vi.waitFor(() => expect(document.activeElement).toBe(cleared[0]));
    expect(posts.filter((post) => post.path === "/api/auth/verify")).toHaveLength(1);

    await act(async () => root.unmount());
  });
});
