import { init, use } from "echarts/core";
import { BarChart, CustomChart, LineChart } from "echarts/charts";
import { GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import { expect,it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { barOption,chartThemeFor,timeseriesOption } from "../../../panels/compile";
import { withAnnotations } from "../../../panels/annotations";
import { nativeAudit } from "./native-audit";
use([BarChart,CustomChart,LineChart,GridComponent,LegendComponent,MarkAreaComponent,MarkLineComponent,TooltipComponent,VisualMapComponent,SVGRenderer]);
it.each([false,true])("P3c/P3d native labels are horizontal and above the actual plot, away from x ticks (%s)",dark=>{
 const el=document.createElement("div");document.body.append(el);const chart=init(el,undefined,{renderer:"svg",width:500,height:248});
 try {
  const panel={id:"t",title:"Latency",viz:"timeseries" as const},theme=chartThemeFor(dark);
  const result={id:"t",status:"ok" as const,elapsed_ms:1,from_ms:0,to_ms:3600000,frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"p95",type:"number" as const,role:"measure" as const}],values:[[0,3600000],[10,20]],rows:2}};
  const option=withAnnotations(timeseriesOption(panel,result,theme),panel,result,{deploys:[{namespace:"shop",service:"cart",version:"2.3.0",at:new Date(1200000).toISOString()}],anomalies:[{namespace:"shop",service:"cart",from:new Date(1800000).toISOString(),to:new Date(3600000).toISOString(),kind:"latency",title:"Slow",severity:"bad"}]},{},theme,{width:500,height:248});
  chart.setOption({...option,animation:false},{notMerge:true});
  const rect=(chart as unknown as {getModel():{getComponent(name:string):{coordinateSystem:{getRect():{y:number;height:number}}}}}).getModel().getComponent("grid").coordinateSystem.getRect();
  const audit=nativeAudit(chart,option,{width:500,height:248});
  for(const name of ["cart 2.3.0","anomaly"]) {
   const texts=audit.texts.filter(t=>t.text===name);expect(texts.length).toBeGreaterThan(0);
   for(const text of texts){expect(text.bottom).toBeLessThan(rect.y);expect(text.top).toBeGreaterThanOrEqual(0);expect(text.right-text.left).toBeGreaterThan(text.bottom-text.top);expect(text.bottom).toBeLessThan(rect.y+rect.height);}
  }
 }finally{chart.dispose();el.remove();}
});
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
