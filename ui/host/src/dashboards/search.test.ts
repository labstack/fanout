import { describe, expect, it } from "vitest";
import { effectiveTime, parseSearch, toSearchParams } from "./search";
import { currentValue } from "../../../panels/variables";
import type { DashboardSpec } from "../../../panels/types";

describe("dashboard search", () => {
  it("keeps only recognised view state", () => {
    expect(parseSearch({ range: "6h", "var-service": "checkout", "var-route": { values: ["a", "b"] }, compare: "1", view: "latency", junk: 1, range2: "x" }))
      .toEqual({ range: "6h", compare: "1", view: "latency", vars: { service: "checkout", route: ["a", "b"] } });
    expect(parseSearch({ range: "90m", from: "2026-10-01T12:00:00Z", to: "nope" })).toEqual({});
    expect(parseSearch({ from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" })).toEqual({ from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" });
  });

  it("round-trips through router params", () => {
    const search = { range: "1h", vars: { service: "checkout" }, edit: "1" as const };
    expect(parseSearch(toSearchParams(search))).toEqual(search);
  });

  it("coerces scalar primitives and rejects repeated variable lists", () => {
    expect(parseSearch({ compare: 1, edit: 1, "var-n": 42, "var-env": true, "var-list": [42, true, "cart"] }))
      .toEqual({ compare: "1", edit: "1", vars: { n: "42", env: "true" } });
    expect(parseSearch({ compare: false, edit: true, view: true, "var-invalid": [null] })).toEqual({ view: "true" });
  });

  it("drops absolute ranges whose from is after to", () => {
    expect(parseSearch({ from: "2026-10-01T13:00:00Z", to: "2026-10-01T12:00:00Z" })).toEqual({});
  });

  it("round-trips an explicit empty array selection", () => {
    expect(parseSearch(toSearchParams({ vars: { service: [] } }))).toEqual({ vars: { service: [] } });
  });

  it("lets the URL override the saved time", () => {
    const spec = { version: 1, name: "x", time: { range: "1h", refresh: "30s" }, panels: [] } as DashboardSpec;
    expect(effectiveTime(spec, {})).toEqual({ range: "1h", refresh: "30s" });
    expect(effectiveTime(spec, { range: "24h" })).toEqual({ range: "24h", refresh: "30s" });
    expect(effectiveTime(spec, { from: "a", to: "b" })).toEqual({ from: "a", to: "b", refresh: "30s" });
  });
});


it("requires RFC3339 times and zones for absolute ranges",()=>{
 for(const from of ["2026-10-05","2026-10-05T00:00:00","10/05/2026","2026-02-30T00:00:00Z","2026-10-05T24:00:00Z","2026-10-05T12:00:00+01:60"]){expect(parseSearch({from,to:"2026-10-06T00:00:00Z"})).toEqual({});}
 expect(parseSearch({from:"2026-10-05T01:00:00+01:00",to:"2026-10-05T01:00:00Z"})).toMatchObject({from:"2026-10-05T01:00:00+01:00"});
});

it('round-trips All, empty, empty-string, one and several selections distinctly',()=>{
  const values:(string|string[])[]=['$__all',[],[''],['checkout'],['checkout','payments']];
  for(const value of values) {
    const vars={service:typeof value==='string'?value:[...value]};
    const raw=toSearchParams({vars});
    const encoded=JSON.parse(JSON.stringify(raw));
    expect(parseSearch(encoded).vars).toEqual(vars);
  }
  expect(parseSearch({'var-service':{values:[],extra:true}}).vars).toBeUndefined();
});
it('rejects malformed, oversized and repeated variable arrays',()=>{
 for(const value of [[],['checkout'],{values:[1]},{values:null},{other:[]},{values:[],extra:true},{values:Array(501).fill('checkout')}]) {
  expect(parseSearch({'var-service':value}).vars).toBeUndefined();
 }
 expect(parseSearch({'var-service':{values:Array(500).fill('checkout')}}).vars?.service).toHaveLength(500);
});

it('validates URL options while preserving an explicit empty selection',()=>{
 const variable={name:'service',kind:'custom' as const,multi:true,options:['checkout','payments'],default:'checkout'};
 expect(currentValue(variable,parseSearch({'var-service':{values:['invalid','payments']}}).vars!)).toEqual(['payments']);
 expect(currentValue(variable,parseSearch({'var-service':{values:[]}}).vars!)).toEqual([]);
 expect(currentValue(variable,parseSearch({'var-service':{values:['invalid']}}).vars!)).toEqual('checkout');
});
