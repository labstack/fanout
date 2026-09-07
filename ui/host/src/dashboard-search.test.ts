import { describe, expect, it } from "vitest";
import { dashboardSearch } from "./dashboard-search";

describe("dashboardSearch", () => {
  it("keeps a window the dashboard actually offers", () => {
    expect(dashboardSearch({ window: "24h" })).toEqual({ window: "24h" });
  });

  it("drops a window nothing can render rather than failing the route", () => {
    expect(dashboardSearch({ window: "7d" })).toEqual({});
    expect(dashboardSearch({ window: 24 })).toEqual({});
  });

  it("keeps a namespace and trims it", () => {
    expect(dashboardSearch({ namespace: " prod " })).toEqual({ namespace: "prod" });
  });

  it("treats a blank namespace as no filter", () => {
    expect(dashboardSearch({ namespace: "   " })).toEqual({});
  });
});
