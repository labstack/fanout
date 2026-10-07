import { init, use } from "echarts/core";
import { BarChart, CustomChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import { expect,it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { barOption,chartThemeFor } from "../../../panels/compile";
import { nativeAudit } from "./native-audit";
use([BarChart,CustomChart,GridComponent,LegendComponent,TooltipComponent,VisualMapComponent,SVGRenderer]);
it.each([false,true])("measures actual SVG renderer heat gaps/scale and bar labels, dark=%s",dark=>{
 const el=document.createElement("div");document.body.append(el);
 const chart=init(el,undefined,{renderer:"svg",width:500,height:240});const theme=chartThemeFor(dark);
 try {
  const result={id:"h",status:"ok" as const,elapsed_ms:1,interval:"1m",frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"bucket_lower",type:"number" as const,role:"dimension" as const,unit:"ms"},{name:"bucket_upper",type:"number" as const,role:"dimension" as const,unit:"ms"},{name:"count",type:"number" as const,role:"measure" as const,unit:"count"}],values:[[0,60000,0,60000],[0,0,25,25],[25,25,50,50],[1,2,3,4]],rows:4}};
  const heat=analysisOption({id:"h",title:"Heat",viz:"heatmap"},result,theme,{width:500,height:240});
  chart.setOption(heat,{notMerge:true});
  const audit=nativeAudit(chart,heat,{width:500,height:240},{width:400,height:160});
  expect(audit.heat).toBeDefined();expect(audit.heat!.gaps.length).toBeGreaterThan(0);expect(audit.heat!.gaps.every(g=>Math.abs(g-1)<.01)).toBe(true);
  expect(audit.heat!.scale_width).toBe(96);expect(audit.heat!.scale_height).toBe(8);
  expect(audit.marks.length).toBeGreaterThan(0);expect(audit.texts.every(t=>t.size>=11)).toBe(true);
  const bars=barOption({id:"b",title:"Bars",viz:"bar"},{columns:[{name:"route",type:"string",role:"dimension"},{name:"latency",type:"number",role:"measure",unit:"ms"}],values:[["/cart","/checkout"],[1450,330]],rows:2},theme);
  chart.setOption({...bars,animation:false},{notMerge:true});
  const bar=nativeAudit(chart,bars,{width:500,height:240});expect(bar.bars!.value_labels).toContain("1.45 s");expect(bar.bars!.value_labels).toContain("330 ms");expect(bar.bars!.category_fraction).toBeLessThanOrEqual(.4);
 } finally {chart.dispose();el.remove();}
});
