import { describe, expect, it } from "vitest";
import { healthBorderType, healthColor, healthSymbol } from "../../chart";
import {serviceMapModel} from "../../panels/rollups";
import {layoutServiceMapRaw} from "./dashboards/viz/service-map-layout";
import {demoFrame} from "../tests/service-map-demo";

describe("status encoding", () => {
  // Health was drawn in hue alone, which is the one channel a reader with a
  // colour vision deficiency does not have.
  it("gives each health state a distinct shape", () => {
    const shapes = ["healthy", "degraded", "unhealthy"].map(healthSymbol);
    expect(new Set(shapes).size).toBe(3);
    // Shape is the severity channel and an ungraded node has no place in it,
    // so it keeps the circle and separates itself by outline and by colour.
    expect(healthSymbol("unknown")).toBe(healthSymbol("healthy"));
    expect(healthBorderType("unknown")).not.toBe(healthBorderType("healthy"));
    expect(healthColor("unknown")).not.toBe(healthColor("healthy"));
  });

  it("preserves unknown health without inferring a verdict from traffic", () => {
    const healthColumn=demoFrame.columns.findIndex(c=>c.name==="health");
    const f=structuredClone(demoFrame);f.values[healthColumn]=f.values[healthColumn].map(()=>"unknown");
    expect(serviceMapModel(f).nodes.every(n=>n.health==="unknown")).toBe(true);
    expect(healthColor("unknown")).toBe("gray");
  });
});

describe("map symbols", () => {
  it("keeps card hit targets equal across health states", () => {
    const widths=[];
    for(const health of ["healthy","degraded","unhealthy"]) {
      const f=structuredClone(demoFrame),index=f.columns.findIndex(c=>c.name==="health");f.values[index]=f.values[index].map(()=>health);
      const layout=layoutServiceMapRaw(serviceMapModel(f),{width:1100,height:300});
      widths.push(layout.nodes.map(n=>[n.width,n.height]));
    }
    expect(widths[0]).toEqual(widths[1]);expect(widths[1]).toEqual(widths[2]);
  });
});
