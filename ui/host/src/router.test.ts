import { describe, expect, it } from "vitest";
import { router } from "./router";
import { parseSearch, toSearchParams } from "./dashboards/search";

describe("router search encoding", () => {
  it("round trips structured variable arrays and rejects repeated variable decoding", () => {
    const search={range:"6h",compare:"1" as const,vars:{service:"checkout",route:["a","b"]}};
    const location=router.buildLocation({to:"/dashboards/$dashboardId",params:{dashboardId:"d"},search:toSearchParams(search)});
    expect(parseSearch(router.options.parseSearch!(location.searchStr))).toEqual(search);
    expect(location.searchStr).toContain("var-route=%7B%22values%22");
    expect(parseSearch(router.options.parseSearch!("?var-route=a&var-route=b")).vars).toBeUndefined();
    expect(router.options.parseSearch!("?tags=a&tags=b")).toEqual({tags:["a","b"]});
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

it('copies full-screen compare and drill links with exact instants and distinct variable selections',()=>{
 const from='2026-10-08T00:00:00.123456789Z',to='2026-10-08T01:00:00.987654321Z';
 const drill=JSON.stringify({panel_id:'logs',kind:'logs',from,to,window_from:from,window_to:to,dimensions:{service:'checkout'}});
 for(const service of ['$__all',[],[''],['checkout'],['checkout','payments'],'', 'a,b','[checkout]','{"literal":true}','{"values":[]}']) {
  const search={from,to,compare:'1' as const,view:'logs',drill,vars:{service}};
  const location=router.buildLocation({to:'/dashboards/$dashboardId',params:{dashboardId:'d'},search:toSearchParams(search)});
  expect(parseSearch(router.options.parseSearch!(new URL(location.href,'https://fanout.test').search))).toEqual(search);
 }
});
