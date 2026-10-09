import {expect,it} from "vitest";
import {chartThemeFor,timeseriesOption} from "../../../panels/compile";
import {withAnnotations} from "../../../panels/annotations";
import type {Panel,PanelResult} from "../../../panels/types";
const panel:Panel={id:"t",title:"Latency",viz:"timeseries"};
const result:PanelResult={id:"t",status:"ok",elapsed_ms:1,from_ms:0,to_ms:3600000,frame:{columns:[{name:"time",type:"time",role:"time"},{name:"p95",type:"number",role:"measure",unit:"ms"}],values:[[0,3600000],[10,20]],rows:2}};
const deploys=[600000,610000,1800000].map(at=>({namespace:"shop",service:"cart",version:"2.3.0",at:new Date(at).toISOString()}));
it.each([false,true])("puts individual horizontal surface chips above the plot and preserves the base (%s)",dark=>{
 const theme=chartThemeFor(dark),base=timeseriesOption(panel,result,theme) as any;
 const got=withAnnotations(base,panel,result,{deploys,anomalies:[]},{},theme,{width:500,height:248}) as any;
 expect(got.grid.top).toBeGreaterThan(base.grid.top);expect(base.grid.top).toBe(12);
 const marks=got.series[0].markLine.data;expect(marks).toHaveLength(3);
 expect(got.graphic.filter((g:any)=>g.annotation).map((g:any)=>g.style.text)).toEqual(["cart 2.3.0","cart 2.3.0","cart 2.3.0"]);
 for(const mark of marks){expect(mark.label).toEqual({show:false});expect(mark.lineStyle.type).toBe("dashed");}
 for(const chip of got.graphic.filter((g:any)=>g.annotation)){expect(chip.style.backgroundColor).toBe(theme.surface);expect(chip.top).toBeLessThan(got.grid.top);}
});
it.each([false,true])("puts anomaly text above the grid and labels even narrow windows once in the shared row (%s)",dark=>{
 const theme=chartThemeFor(dark),base=timeseriesOption(panel,result,theme) as any;
 const anomaly={namespace:"shop",service:"cart",title:"Slow",severity:"bad",kind:"latency",from:new Date(900000).toISOString(),to:new Date(1800000).toISOString()};
 const size={width:500,height:248,measureText:(text:string)=>text.length*7};
 const got=withAnnotations(base,panel,result,{deploys:[],anomalies:[anomaly]},{},theme,size) as any;
 expect(got.grid.top).toBeGreaterThan(base.grid.top);
 const area=got.series[0].markArea.data[0][0];expect(got.graphic.some((g:any)=>g.annotation && g.style.text==="anomaly")).toBe(true);expect(area.label.show).toBe(false);expect(area.tooltip.formatter()).toContain("Slow · bad");
 const narrow=withAnnotations(base,panel,result,{deploys:[],anomalies:[{...anomaly,to:new Date(901000).toISOString()}]},{},theme,size) as any;
 expect(narrow.series[0].markArea.data[0][0].label).toEqual({show:false});expect(narrow.graphic.filter((g:any)=>g.annotation)).toHaveLength(1);expect(narrow.series[0].markArea.data[0][0].tooltip.formatter()).toContain("Slow");
});
