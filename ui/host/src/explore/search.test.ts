import { describe, expect, it } from "vitest";
import { exploreTime, parseExploreSearch, toExploreParams, type ExploreSearch } from "./search";

describe("explore search", () => {
  it("round-trips every field", () => {
    const search: ExploreSearch = { q: "spans | count() by service", viz: "bar", range: "1h", from: "2026-10-30T10:00:00Z", to: "2026-10-30T11:00:00Z", tab: "sql", sql: "SELECT 1" };
    expect(parseExploreSearch(toExploreParams(search))).toEqual(search);
    const relative: ExploreSearch = { q: "logs", range: "24h", tab: "query" };
    expect(toExploreParams(relative)).toEqual({ v: "1", q: "logs", range: "24h" });
    expect(parseExploreSearch(toExploreParams(relative))).toEqual(relative);
  });

  it("defaults and drops what it cannot use", () => {
    expect(parseExploreSearch({})).toEqual({ q: "", range: "1h", tab: "query" });
    expect(parseExploreSearch({ v: "2", q: "spans" })).toEqual({ q: "", range: "1h", tab: "query" });
    expect(parseExploreSearch({ v: 1, q: "spans", viz: "pie", range: "9h", tab: "chat", from: "2026-10-30T11:00:00Z", to: "2026-10-30T10:00:00Z" })).toEqual({ q: "spans", range: "1h", tab: "query" });
  });

  it("keeps long text for the server to bound", () => {
    const q = "spans where " + "service:a or ".repeat(2000) + "service:b";
    expect(parseExploreSearch({ v: "1", q }).q).toBe(q);
  });

  it("prefers an absolute window", () => {
    expect(exploreTime({ q: "", range: "3h", tab: "query" })).toEqual({ range: "3h", refresh: "off" });
    expect(exploreTime({ q: "", range: "3h", tab: "query", from: "a", to: "b" })).toEqual({ from: "a", to: "b", refresh: "off" });
  });
});
