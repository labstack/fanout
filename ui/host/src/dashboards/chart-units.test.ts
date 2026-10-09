import { expect, it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { barOption, chartThemeFor, gaugeOption, timeseriesOption } from "../../../panels/compile";
import { typeScale, fonts } from "../../../tokens";
import { relativeLuminance } from "../../../theme";
import { withAnnotations } from "../../../panels/annotations";
import { seriesSlot } from "../../../chart";
import { formatBucket } from "../../../panels/units";
import type { Frame, PanelResult } from "../../../panels/types";

const heat: PanelResult = { id: "h", status: "ok", elapsed_ms: 1, interval: "1m", frame: {
  columns: [{name:"time",type:"time",role:"time"},{name:"bucket_lower",type:"number",role:"dimension",unit:"ms"},{name:"bucket_upper",type:"number",role:"dimension",unit:"ms"},{name:"count",type:"number",role:"measure",unit:"count"}],
  values: [[0,0,0],[0,25,3200],[25,50,null],[1,10,100]], rows: 3,
} };

it.each([false,true])("discrete full-height heat cells and single-hue inline ramp, dark=%s", dark => {
  const theme = chartThemeFor(dark);
  const option = analysisOption({id:"h",title:"Heat",viz:"heatmap"},heat,theme) as any;
  const cell = option.series[0].renderItem({}, {value:(i:number)=>[0,0,10,60000][i],coord:(v:number[])=>[v[0]/1000,20],size:()=>[60,20],visual:()=>"#fff"});
  // Q1 uses a surface gap without a uniform identity-colour outline.
  expect(cell.shape).toMatchObject({x:.5,y:10.5,width:59,height:19});
  expect(option.visualMap.inRange.color).toHaveLength(7);
  expect(option.visualMap.inRange.color.at(-1)).toBe(dark ? "#7dd3fc" : "#1d4ed8");
  expect(option.visualMap.itemWidth).toBe(8);
  expect(option.grid.bottom).toBeLessThanOrEqual(32);
  expect(option.yAxis.data).toEqual(["<25 ms","25–50 ms","≥3.2 s"]);
});

it("duration buckets retain decimal seconds and open bounds", () => {
  expect(formatBucket(null,25,"ms")).toBe("<25 ms");
  expect(formatBucket(800,1600,"ms")).toBe("800–1,600 ms");
  expect(formatBucket(1600,3200,"ms")).toBe("1.6–3.2 s");
  expect(formatBucket(3200,null,"ms")).toBe("≥3.2 s");
});

const path = "/oteldemo.CartService/some/long/path/to/EmptyCart";
const bars: Frame = {columns:[{name:"route",type:"string",role:"dimension"},{name:"latency",type:"number",role:"measure",unit:"ms"}],values:[[path,"/checkout"],[1450,330]],rows:2};
it.each([false,true])("adaptive bar labels and bounded middle-ellipsis, dark=%s", dark => {
  const theme = chartThemeFor(dark);
  const option = barOption({id:"b",title:"Bars",viz:"bar"},bars,theme,{width:300,height:220,measureText:t=>t.length*7}) as any;
  expect(option.yAxis.axisLabel.width).toBeLessThanOrEqual(120);
  const label = option.yAxis.axisLabel.formatter(path);
  expect(label).toContain("…"); expect(label.startsWith("/otelde")).toBe(true); expect(label.endsWith("EmptyCart")).toBe(true);
  expect(option.tooltip.formatter({name:path,seriesName:"latency",value:1450})).toContain(path);
  expect(option.series[0].itemStyle.color).toBe(seriesSlot(0,dark));
  expect(option.series[0].itemStyle.borderRadius).toEqual([0,4,4,0]);
  expect(option.series[0].label.formatter({value:1450})).toBe("1.45 s");
  expect(option.series[0].label.formatter({value:330})).toBe("330 ms");
  expect([0,300,1200].map(option.xAxis.axisLabel.formatter)).toEqual(["0 ms","300 ms","1.2 s"]);
});
it.each([false,true])("deploy split uses a thinner grey before and labelled slot-zero since, dark=%s", dark => {
  const frame: Frame = {...bars,columns:[bars.columns[0],{name:"period",type:"string",role:"dimension"},bars.columns[1]],values:[[path,path],["Before deploy","Since deploy"],[50,1450]],rows:2,periods:{"Before deploy":{from:"2026-10-07T00:00:00Z",to:"2026-10-07T00:30:00Z"},"Since deploy":{from:"2026-10-07T00:30:00Z",to:"2026-10-07T01:00:00Z"}}};
  const theme = chartThemeFor(dark), option = barOption({id:"b",title:"Split",viz:"bar"},frame,theme) as any;
  expect(option.legend.left).toBe(0);
  expect(option.series[0].itemStyle.color).toBe(theme.muted);
  expect(option.series[0].barMaxWidth).toBeLessThan(option.series[1].barMaxWidth);
  expect(option.series[0].label.show).toBe(false); expect(option.series[1].label.show).toBe(true);
  expect(option.series[1].itemStyle.color).toBe(seriesSlot(0,dark));
  expect(option.series[1].data[0].selection.from).toBe(frame.periods!["Since deploy"].from);
});

it.each([false,true])("every axis is muted mono 11–12, gauges respect the micro floor, dark=%s", dark=>{
 const theme=chartThemeFor(dark);
 const scatter:PanelResult={...heat,frame:{columns:[{name:"service",type:"string",role:"dimension"},{name:"x",type:"number",role:"measure",unit:"count"},{name:"y",type:"number",role:"measure",unit:"ms"}],values:[["s"],[3],[80]],rows:1}};
 const options=[barOption({id:"b",title:"Bars",viz:"bar"},bars,theme),timeseriesOption({id:"t",title:"Time",viz:"timeseries"},heat,theme),...(["heatmap","histogram","state_timeline","scatter"] as const).map(viz=>analysisOption({id:"a",title:"Analysis",viz},viz==="scatter"?scatter:heat,theme))] as any[];
 for(const option of options) for(const axis of [option.xAxis,option.yAxis].flat()) {
  expect(axis.axisLabel.fontFamily).toBe(fonts.display); expect(axis.axisLabel.fontSize).toBeGreaterThanOrEqual(11);expect(axis.axisLabel.fontSize).toBeLessThanOrEqual(12);expect(axis.axisLabel.color).toBe(theme.muted);
  if(axis.splitLine?.show!==false) expect(axis.splitLine.lineStyle.color).toBe(theme.grid);
 }
 const option=gaugeOption({id:"g",title:"Gauge",viz:"gauge",min:0,max:100,thresholds:[{value:50,status:"bad"}]},99,theme,"percent",{width:100,height:60}) as any;
 expect(option.graphic.find((item:any)=>item.id==="gauge-value").style.fontSize).toBeGreaterThanOrEqual(24);
 for(const item of option.graphic.filter((g:any)=>g.type==="text")) expect(item.style.fontSize).toBeGreaterThanOrEqual(typeScale.micro);
});

const contrast=(a:string,b:string)=>{const x=relativeLuminance(a),y=relativeLuminance(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);};
it.each([false,true])("all categorical marks have 3:1 fill or outline without changing palette, dark=%s",dark=>{
 const theme=chartThemeFor(dark);
 expect(contrast(theme.muted,theme.surface)).toBeGreaterThanOrEqual(4.5);expect(contrast(theme.text,theme.surface)).toBeGreaterThanOrEqual(4.5);
 const f:Frame={columns:[bars.columns[0],...Array.from({length:6},(_,i)=>({name:`m${i}`,type:"number" as const,role:"measure" as const}))],values:[["s"],...[1,2,3,4,5,6].map(n=>[n])],rows:1};
 const option=barOption({id:"b",title:"Marks",viz:"bar"},f,theme) as any;
 for(const [i,s] of option.series.entries()) {
  expect(s.itemStyle.color).toBe(seriesSlot(i,dark));
  const paint=contrast(s.itemStyle.color,theme.surface)>=3 ? s.itemStyle.color : s.itemStyle.borderColor;
  expect(paint).toBeDefined();expect(contrast(paint,theme.surface)).toBeGreaterThanOrEqual(3);
  if(paint!==s.itemStyle.color) expect(s.itemStyle.borderWidth).toBeGreaterThanOrEqual(1);
 }
 const heatOption=analysisOption({id:"h",title:"Heat",viz:"heatmap"},heat,theme) as any;
 const cell=heatOption.series[0].renderItem({}, {value:(i:number)=>[0,0,10,60000][i],coord:(v:number[])=>[v[0]/1000,20],size:()=>[60,20],visual:()=>theme.surface});
 expect(cell.style.lineWidth).toBe(0);expect(cell.style.stroke).toBeUndefined();
 expect(cell.shape).toMatchObject({x:.5,y:10.5,width:59,height:19});
});

it.each([false,true])("threshold/anomaly text stays readable over the warm tint, dark=%s",dark=>{
 const theme=chartThemeFor(dark),panel={id:"t",title:"Time",viz:"timeseries" as const,thresholds:[{value:1,status:"warn" as const,label:"Budget"}]};
 const result={...heat,from_ms:0,to_ms:120000};
 const base=timeseriesOption(panel,result,theme) as any;
 const option=withAnnotations(base,panel,result,{deploys:[],anomalies:[{namespace:"",service:"s",kind:"latency",from:new Date(0).toISOString(),to:new Date(120000).toISOString(),title:"Slow",severity:"warn"}]},{},theme) as any;
 const threshold=base.series[0].markLine.data[0].label,anomaly=option.graphic.find((g:any)=>g.annotation).style;
 expect(anomaly.stroke).toBe(theme.surface);expect(anomaly.lineWidth).toBeGreaterThanOrEqual(3);expect(contrast(anomaly.fill,anomaly.stroke)).toBeGreaterThanOrEqual(4.5);
 for(const label of [threshold]) {expect(label.textBorderColor).toBe(theme.surface);expect(label.textBorderWidth).toBeGreaterThanOrEqual(3);expect(contrast(label.color,label.textBorderColor)).toBeGreaterThanOrEqual(4.5);}
});
