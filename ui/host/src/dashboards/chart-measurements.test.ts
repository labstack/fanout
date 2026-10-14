import { init, use, setPlatformAPI } from "echarts/core";
import { BarChart, CustomChart, ScatterChart, LineChart } from "echarts/charts";
import { GraphicComponent, GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import { expect,it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { gaugeOption,barOption,chartThemeFor,timeseriesOption } from "../../../panels/compile";
import { withAnnotations } from "../../../panels/annotations";
import { nativeAudit } from "./chart-measurements";
// SVG in happy-dom has no canvas/font engine. Give the native renderer a
// deterministic monospace metric rather than zrender's one-em-per-character fallback.
setPlatformAPI({measureText: (text, font) => ({width: Array.from(text ?? "").length * Number(font?.match(/([\d.]+)px/)?.[1] ?? 12) * .62})});
use([BarChart,CustomChart,ScatterChart,LineChart,GraphicComponent,GridComponent,LegendComponent,MarkAreaComponent,MarkLineComponent,TooltipComponent,VisualMapComponent,SVGRenderer]);
it.each([false,true])("native gauge text never overlaps in meter layouts at 1100/1440 layouts (%s)",dark=>{
 for(const size of [{width:159,height:56},{width:238,height:56},{width:159,height:180},{width:220,height:140},{width:238,height:180},{width:320,height:180}]) {
  const el=document.createElement("div");document.body.append(el);const chart=init(el,undefined,{renderer:"svg",...size});
  try {
   const option=gaugeOption({id:"g",title:"Gauge",viz:"gauge",min:0,max:10,thresholds:[{value:1,status:"warn"}]},1.76,chartThemeFor(dark),"percent",size);
   chart.setOption(option,{notMerge:true});const audit=nativeAudit(chart,option,size);
   expect(audit.gauge?.mode).toBe("linear");
   expect(audit.texts.find(t=>t.text==="1.76%")!.size).toBeGreaterThanOrEqual(28);
   for(const [i,t] of audit.texts.entries()) {
    expect(t.left).toBeGreaterThanOrEqual(0);expect(t.right).toBeLessThanOrEqual(size.width);expect(t.top).toBeGreaterThanOrEqual(0);expect(t.bottom).toBeLessThanOrEqual(size.height);
    for(const other of audit.texts.slice(i+1))expect(t.left<other.right&&t.right>other.left&&t.top<other.bottom&&t.bottom>other.top,JSON.stringify([t,other])).toBe(false);
   }
  }finally{chart.dispose();el.remove();}
 }
});
it.each([false,true])("native labels are horizontal and above the actual plot, away from x ticks (%s)",dark=>{
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

it.each([false,true])("native renderer keeps five heat intensities and excludes empty cells (%s)",dark=>{
 const el=document.createElement("div");document.body.append(el);const chart=init(el,undefined,{renderer:"svg",width:600,height:248});
 try {
  const counts=[0,1,10,100,1000,3000];
  const result={id:"h",status:"ok" as const,elapsed_ms:1,interval:"1m",frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"bucket_lower",type:"number" as const,role:"dimension" as const},{name:"bucket_upper",type:"number" as const,role:"dimension" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[counts.map((_,i)=>i*60000),counts.map(()=>0),counts.map(()=>25),counts],rows:6}};
  const option=analysisOption({id:"h",title:"Heat",viz:"heatmap"},result,chartThemeFor(dark));chart.setOption(option,{notMerge:true});
  const audit=nativeAudit(chart,option,{width:600,height:248});
  expect(audit.marks).toHaveLength(5);expect(new Set(audit.marks.map(m=>m.fill)).size).toBe(5);
  for(const text of ["1","3k"]) expect(audit.texts.filter(t=>t.text===text)).toHaveLength(1);
  expect(audit.texts.some(t=>["10","100"].includes(t.text))).toBe(false);
  const ticks=audit.texts.filter(t=>["1","3k"].includes(t.text));
  for(const tick of ticks) {expect(tick.left).toBeGreaterThanOrEqual(0);expect(tick.right).toBeLessThanOrEqual(600);expect(tick.top).toBeGreaterThanOrEqual(0);expect(tick.bottom).toBeLessThanOrEqual(248);}
 }finally{chart.dispose();el.remove();}
});

it.each([false,true])("native small gauges and m-height plots have unclipped, nonoverlapping labels (%s)",dark=>{
 const overlaps=(a:{left:number;top:number;right:number;bottom:number},b:typeof a)=>a.left<b.right&&a.right>b.left&&a.top<b.bottom&&a.bottom>b.top;
 const el=document.createElement("div");document.body.append(el);const chart=init(el,undefined,{renderer:"svg",width:238,height:56});
 const theme=chartThemeFor(dark);
 try {
  for(const width of [159,238]) {
   chart.resize({width,height:56});
   const option=gaugeOption({id:"g",title:"Gauge",viz:"gauge",min:0,max:10,thresholds:[{value:1,status:"warn"}]},1.74,theme,"percent",{width,height:56});
   chart.setOption(option,{notMerge:true});const gaugeAudit=nativeAudit(chart,option,{width,height:56});expect(gaugeAudit.gauge!.mode).toBe("linear");const texts=gaugeAudit.texts;
   expect(texts.find(t=>t.text==="1.74%")?.size).toBeGreaterThanOrEqual(24);
   for(const [i,text] of texts.entries()) {expect(text.top).toBeGreaterThanOrEqual(0);expect(text.bottom).toBeLessThanOrEqual(56);expect(text.left).toBeGreaterThanOrEqual(0);expect(text.right).toBeLessThanOrEqual(width);for(const other of texts.slice(i+1))expect(overlaps(text,other),JSON.stringify([text,other])).toBe(false);}
  }
  for(const width of [400,520]) for(const viz of ["timeseries","scatter","histogram","heatmap","state_timeline"] as const) {
   const height=210;chart.resize({width,height});
   const columns=[{name:"time",type:"time" as const,role:"time" as const},{name:"service",type:"string" as const,role:"dimension" as const},{name:"calls",type:"number" as const,role:"measure" as const},{name:"p95",type:"number" as const,role:"measure" as const,unit:"ms"}];
   const frame={columns,values:[[0,60000,120000,180000,240000,300000],["load-generator","frontend-proxy","product-catalog","fraud-detection","product-reviews","recommendation"],[1,2,3,4,5,6],[10,20,30,40,50,60]],rows:6};
   const panel={id:"t",title:"Chart",viz},result={id:"t",status:"ok" as const,elapsed_ms:1,interval:"1m",from_ms:0,to_ms:360000,frame};
   const option=viz==="timeseries"?timeseriesOption(panel,result,theme,{width,height}):analysisOption(panel,result,theme,{width,height});
   const compiled=withAnnotations(option,panel,result,{deploys:[{service:"cart",namespace:"",version:"2.3",at:new Date(240000).toISOString()}],anomalies:[]},{},theme,{width,height});
   chart.setOption({...compiled,animation:false},{notMerge:true});
   const rect=(chart as any).getModel().getComponent("grid").coordinateSystem.getRect();expect(rect.height/height,`${viz} at ${width}`).toBeGreaterThanOrEqual(.55);
   const audit=nativeAudit(chart,compiled,{width,height},rect);expect(audit.plot!.y_ticks.length).toBeGreaterThan(0);
   const ticks=audit.texts.filter(t=>t.right<=rect.x&&t.top>=rect.y-6&&t.bottom<=rect.y+rect.height+6);
   for(const [i,t] of ticks.entries())for(const other of ticks.slice(i+1))expect(overlaps(t,other),`${viz} tick overlap`).toBe(false);
   if(viz==="scatter"||viz==="histogram")for(const name of viz==="scatter"?["calls","p95"]:["count"]) {const title=audit.texts.find(t=>t.text===name)!;expect(title).toBeDefined();expect(title.left).toBeGreaterThanOrEqual(0);expect(title.right).toBeLessThanOrEqual(width);expect(title.top).toBeGreaterThanOrEqual(0);expect(title.bottom).toBeLessThanOrEqual(height);expect(overlaps(title,{left:rect.x,top:rect.y,right:rect.x+rect.width,bottom:rect.y+rect.height})).toBe(false);for(const tick of ticks)expect(overlaps(title,tick)).toBe(false);}
  }
 }finally{chart.dispose();el.remove();}
});
