import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { HomePage } from "./home-page";

function renderHome() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><HomePage /></MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => vi.restoreAllMocks());

describe("HomePage", () => {
  it("shows the summary line on success", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      health: { score: 1, total_services: 12, by_status: { healthy: 12 }, throughput_per_min: 2712000, global_error_rate: 0.0021, global_p95_ms: 142 },
      services: [{ service: "checkout", status: "healthy", health_score: 1, requests: 10, traffic_per_min: 60, error_rate: 0, p50_ms: 1, p95_ms: 2 }],
      incidents: [],
      activity: { buckets: [] },
      recent_errors: [],
      recent_errors_unavailable: false,
      alerts: { status: "ok", items: [] },
    }), { status: 200 })));
    renderHome();
    expect(await screen.findByRole("heading", { name: /12 services healthy/ })).toBeInTheDocument();
  });

  it("degrades to an error message (never blank) on failure", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("boom", { status: 500 })));
    renderHome();
    expect(await screen.findByRole("alert")).toHaveTextContent(/couldn't load/i);
  });
});
