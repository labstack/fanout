import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { configString, useLastUpdated, useObservability, widgetParams, windowName } from "./data";

describe("widget params", () => {
  it("builds the observability query from filters and config", () => {
    const params = widgetParams({ window: "6h", namespace: "prod" }, { service: "orders", severity: "ERROR", search: "timeout" }, ["service", "severity", "search"]);
    expect(params.get("window")).toBe("6h");
    expect(params.get("namespace")).toBe("prod");
    expect(params.get("limit")).toBe("40");
    expect(params.get("service")).toBe("orders");
    expect(params.get("severity")).toBe("ERROR");
    expect(params.get("search")).toBe("timeout");
    expect(widgetParams({ window: "1h", namespace: "" }).has("namespace")).toBe(false);
    expect(widgetParams({ window: "1h", namespace: "" }, { service: "orders" }).has("service")).toBe(false);
  });

  it("names windows and reads string config", () => {
    expect(windowName("1h")).toBe("1 hour");
    expect(windowName("720h")).toBe("30 days");
    expect(windowName("9h")).toBe("9h");
    expect(configString({ service: "orders" }, "service")).toBe("orders");
    expect(configString({ service: 12 }, "service")).toBe("");
    expect(configString(undefined, "service")).toBe("");
  });
});

describe("useObservability and useLastUpdated", () => {
  const fetchMock = vi.fn<typeof fetch>();
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ data: { health: "healthy" } }), { status: 200, headers: { "content-type": "application/json" } }));
  });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("fetches the endpoint and reports the newest update time", async () => {
    function Probe() {
      const overview = useObservability<{ health: string }>("overview", new URLSearchParams({ window: "1h" }));
      const updated = useLastUpdated();
      return <div>{overview.data?.data.health ?? "loading"} {updated ? "updated" : "never"}</div>;
    }
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => root.render(<QueryClientProvider client={client}><Probe /></QueryClientProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("healthy updated"));
    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/observability/overview?window=1h");
    await act(async () => root.unmount());
  });

  // Opening /dashboards renders the default dashboard and then navigates to
  // its own URL, which unmounts the pane and mounts a new one. Every widget
  // asked the server again on that second mount.
  it("does not ask again when a pane is remounted moments later", async () => {
    function Probe() {
      const overview = useObservability<{ health: string }>("overview", new URLSearchParams({ window: "1h" }));
      return <div>{overview.data?.data.health ?? "loading"}</div>;
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const mount = async () => {
      const container = document.createElement("div");
      document.body.append(container);
      const root = createRoot(container);
      await act(async () => root.render(<QueryClientProvider client={client}><Probe /></QueryClientProvider>));
      await vi.waitFor(() => expect(container.textContent).toContain("healthy"));
      return root;
    };
    const first = await mount();
    await act(async () => first.unmount());
    const second = await mount();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => second.unmount());
  });
});
