import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useVariableOptions } from "./use-variables";
import type { DashboardSpec } from "../../../panels/types";
import { describe, expect, it, vi } from "vitest";
import { ALL, type Variable } from "../../../panels/types";
import { currentValue } from "../../../panels/variables";

const variable: Variable = { name: "service", kind: "query" };
const options = [{ value: "checkout" }, { value: "cart" }];

describe("variable value selection", () => {
  it("treats an empty single selection as unset before options load", () => {
    expect(currentValue({ ...variable, default: "cart" }, { service: [] })).toBe("cart");
    expect(currentValue(variable, { service: [] }, options)).toBe("checkout");
    expect(currentValue(variable, { service: [] })).toBe("");
    expect(currentValue({ ...variable, multi: true }, { service: [] })).toEqual([]);
  });
  it("falls back from All without include_all to the default, then the first option", () => {
    expect(currentValue({ ...variable, default: "cart" }, { service: ALL }, options)).toBe("cart");
    expect(currentValue(variable, { service: ALL }, options)).toBe("checkout");
  });

  it("falls back from a single value missing from loaded options", () => {
    expect(currentValue(variable, { service: "missing" }, options)).toBe("checkout");
    expect(currentValue({ ...variable, default: "cart" }, { service: "missing" }, options)).toBe("cart");
    expect(currentValue({ ...variable, include_all: true }, { service: "missing" }, options)).toBe(ALL);
  });

  it("drops invalid multi values and falls back when none remain", () => {
    expect(currentValue({ ...variable, multi: true }, { service: ["missing", "cart"] }, options)).toEqual(["cart"]);
    expect(currentValue({ ...variable, multi: true }, { service: ["missing"] }, options)).toBe("checkout");
    expect(currentValue({ ...variable, multi: true }, { service: [] }, options)).toEqual([]);
  });

  it("uses server precedence and preserves provided values until options load", () => {
    expect(currentValue({ ...variable, kind: "constant", value: "fixed" }, { service: "cart" }, options)).toBe("fixed");
    expect(currentValue({ ...variable, default: "cart", include_all: true }, {}, options)).toBe("cart");
    expect(currentValue({ ...variable, include_all: true }, {}, options)).toBe(ALL);
    expect(currentValue(variable, {}, options)).toBe("checkout");
    expect(currentValue(variable, { service: "cart" })).toBe("cart");
    expect(currentValue(variable, { service: "cart" }, [])).toBe("");
    expect(currentValue({ name: "text", kind: "text" }, { text: "free text" }, [])).toBe("free text");
    expect(currentValue({ ...variable, kind: "custom", options: ["checkout"] }, { service: "missing" })).toBe("checkout");
  });
});

it("handles null options for saved single and multi selections", () => {
  const response = JSON.parse('{"service":null}');
  expect(currentValue(variable, {service:"checkout"}, response.service)).toBe("");
  expect(currentValue({...variable,multi:true}, {service:["checkout"]}, response.service)).toBe("");
});

const wire = vi.hoisted(() => ({resolve:vi.fn()}));
it.each([{service:null},{}])("normalizes loaded null or missing options %j for saved selections",async response=>{
 vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);
 wire.resolve.mockResolvedValue(response);
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const node=document.createElement("div");const root=createRoot(node);
 const spec:DashboardSpec={version:1,name:"Empty",time:{range:"1h"},variables:[variable],panels:[]};
 let loaded: {value:string}[] | undefined;
 function Host(){const options=useVariableOptions("d",spec,spec.time,{service:"checkout"},wire.resolve);loaded=options.currentData?.service;return null;}
 try{
  await act(async()=>root.render(createElement(QueryClientProvider,{client},createElement(Host))));
  await act(async()=>{await vi.waitFor(()=>expect(loaded).toEqual([]),{interval:5});});
  expect(currentValue(variable,{service:"checkout"},loaded)).toBe("");
  expect(currentValue({...variable,multi:true},{service:["checkout"]},loaded)).toBe("");
 }finally{await act(async()=>root.unmount());client.clear();vi.unstubAllGlobals();}
});
