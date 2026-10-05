import { describe, expect, it } from "vitest";
import { router } from "./router";
import { parseSearch, toSearchParams } from "./dashboards/search";

describe("router search encoding", () => {
  it("round trips dashboard strings and repeated variables in readable URLs", () => {
    const query = "?range=6h&compare=1&var-service=checkout&var-route=a&var-route=b";
    const raw = router.options.parseSearch!(query);
    expect(raw).toEqual({ range: "6h", compare: "1", "var-service": "checkout", "var-route": ["a", "b"] });
    const search = toSearchParams(parseSearch(raw));
    const location = router.buildLocation({ to: "/dashboards/$dashboardId", params: { dashboardId: "d" }, search });
    expect(location.searchStr).toBe(query);
    expect(location.href).not.toContain("%22");
    expect(router.options.parseSearch!(location.searchStr)).toEqual(raw);
  });

  it.each(["/chat", "/chat/$threadId", "/settings"] as const)("preserves %s params and JSON objects without coercing primitives", (to) => {
    const search = { prompt: 'Inspect "checkout" & errors + latency', compare: "1", enabled: "true", value: "null", empty: "", tags: ["a,b", "a&b"], context: { service: "checkout", limit: 1 } };
    const location = router.buildLocation({ to, params: { threadId: "thread-123" }, search });
    expect(router.options.parseSearch!(location.searchStr)).toEqual(search);
    expect(location.searchStr).toContain("compare=1");
    expect(location.searchStr).toContain("enabled=true");
    if (to === "/chat/$threadId") expect(location.pathname).toBe("/chat/thread-123");
  });
});
