import { act, type ComponentType } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { DashboardSearch } from "./search";
import "../routes/dashboards.$dashboardId";

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), component: undefined as ComponentType | undefined }));
vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (options: { component: ComponentType }) => {
    mocks.component = options.component;
    return { useParams: () => ({ dashboardId: "cart" }), useSearch: () => ({ range: "1h" }) };
  },
  useNavigate: () => mocks.navigate,
}));
vi.mock("./page", () => ({
  DashboardPage: ({ onSearch, onOpen }: { onSearch(next: DashboardSearch, replace?: boolean): void; onOpen(id: string): void }) => <>
    <button onClick={() => onSearch({ range: "1h", drill: "trace" }, false)}>Drill</button>
    <button onClick={() => onSearch({ range: "1h", vars: { service: "cart" } }, true)}>Variable</button>
    <button onClick={() => onSearch({ from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" }, false)}>Brush</button>
    <button onClick={() => onSearch({ range: "1h", view: "errors" })}>Panel view</button>
    <button onClick={() => onOpen("checkout")}>Another dashboard</button>
  </>,
}));

afterEach(() => { mocks.navigate.mockClear(); document.body.innerHTML = ""; });
it("preserves scroll for drill, variable, brush and panel-view search updates", async () => {
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  const Detail = mocks.component!;
  try {
    await act(async () => root.render(<Detail />));
    for (const button of [...host.querySelectorAll("button")].slice(0, 4)) {
      await act(async () => button.click());
      expect(mocks.navigate.mock.lastCall?.[0]).toMatchObject({ params: { dashboardId: "cart" }, resetScroll: false });
    }
    expect(mocks.navigate.mock.calls[0][0].replace).toBe(false);
    expect(mocks.navigate.mock.calls[1][0].replace).toBe(true);
    expect(mocks.navigate.mock.calls[2][0].search).toEqual({ from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" });
    await act(async () => host.querySelectorAll("button")[4].click());
    expect(mocks.navigate.mock.lastCall?.[0]).toMatchObject({ params: { dashboardId: "checkout" }, search: {} });
    expect(mocks.navigate.mock.lastCall?.[0].resetScroll).not.toBe(false);
  } finally { await act(async () => root.unmount()); }
});
