import { expect, it } from "vitest";
import { init, use } from "echarts/core";
import { CustomChart } from "echarts/charts";
import { GridComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import { analysisOption } from "../../../panels/analysis";
import { chartThemeFor } from "../../../panels/compile";
import { nativeAudit } from "./chart-measurements";

use([CustomChart, GridComponent, TooltipComponent, VisualMapComponent, SVGRenderer]);
const frame = (columns:number, bands=1) => {
 const rows=Array.from({length:columns*bands},(_,i)=>[Math.floor(i/bands)*86400000/columns,i%bands,1+(i%bands),10+(i%20)]);
 return {id:"h",status:"ok" as const,elapsed_ms:1,interval:`${1440/columns}m`,frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"bucket_lower",type:"number" as const,role:"dimension" as const},{name:"bucket_upper",type:"number" as const,role:"dimension" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[0,1,2,3].map(i=>rows.map(row=>row[i])),rows:rows.length}};
};
for(const dark of [false,true])for(const width of [1,6,7.99,8,12])it(`gaps depend on actual cell width ${width}px, dark=${dark}`,()=>{
 const option=analysisOption({id:"h",title:"Heat",viz:"heatmap"},frame(72),chartThemeFor(dark)) as any;
 const cell=option.series[0].renderItem({}, {value:(i:number)=>[0,0,10,1200000][i],coord:(v:number[])=>[v[0]===0?0:width,20],size:()=>[width,10],visual:()=>"#fff"});
 const gap=width>=8?1:0;
 expect(cell.shape).toMatchObject({x:gap/2,y:15+gap/2,width:width-gap,height:10-gap});
 expect(option.visualMap.dimension).toBe(5);expect(cell.style.stroke).toBeUndefined();
});
for(const dark of [false,true])for(const height of [180,520])it(`only alternates crowded y bucket labels at ${height}px, dark=${dark}`,()=>{
 const option=analysisOption({id:"h",title:"Heat",viz:"heatmap"},frame(72,18),chartThemeFor(dark),{width:800,height}) as any;
 expect(option.yAxis.axisLabel.interval).toBe(height===180?1:0);
 const el=document.createElement("div");document.body.append(el);const chart=init(el,undefined,{renderer:"svg",width:800,height});
 try {
  chart.setOption(option,{notMerge:true});const audit=nativeAudit(chart,option,{width:800,height});
  const labels=audit.texts.filter(t=>option.yAxis.data.includes(t.text));expect(labels).toHaveLength(height===180?9:18);
  const sorted=labels.sort((a,b)=>a.top-b.top);for(let i=1;i<sorted.length;i++)expect(sorted[i].top).toBeGreaterThanOrEqual(sorted[i-1].bottom);
 }finally{chart.dispose();el.remove();}
});
for(const dark of [false,true])for(const columns of [72,288])it(`native cells measure width and adaptive gaps for ${columns} columns, dark=${dark}`,()=>{
 const width=columns===72?800:500,height=248,option=analysisOption({id:"h",title:"Heat",viz:"heatmap"},frame(columns,2),chartThemeFor(dark),{width,height});
 const el=document.createElement("div");document.body.append(el);const chart=init(el,undefined,{renderer:"svg",width,height});
 try {
  chart.setOption(option,{notMerge:true});const audit=nativeAudit(chart,option,{width,height});expect(audit.heat!.cells).toHaveLength(columns*2);
  if(columns===72)expect(Math.min(...audit.heat!.cells.map(c=>c.box.right-c.box.left))).toBeGreaterThanOrEqual(6);
  expect(audit.heat!.gaps.length).toBeGreaterThan(0);expect(audit.heat!.gaps.every(g=>Math.abs(g-(columns===72?1:0))<.01)).toBe(true);
 }finally{chart.dispose();el.remove();}
});
