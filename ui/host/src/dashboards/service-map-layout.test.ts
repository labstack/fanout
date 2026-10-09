import { expect, it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { chartThemeFor } from "../../../panels/compile";
import { relativeLuminance } from "../../../theme";
import { seriesSlot } from "../../../chart";

it.each([false, true])("heat counts use distinct monotonic quantile colours and omit zero (%s)", dark => {
  const theme = chartThemeFor(dark);
  const counts = [0, 1, 10, 100, 1000, 3000];
  const result = { id: "h", status: "ok" as const, elapsed_ms: 1, interval: "1m", frame: {
    columns: [{name:"time",type:"time" as const,role:"time" as const},{name:"bucket_lower",type:"number" as const,role:"dimension" as const},{name:"bucket_upper",type:"number" as const,role:"dimension" as const},{name:"count",type:"number" as const,role:"measure" as const}],
    values: [counts.map((_, i) => i * 60000), counts.map(() => 0), counts.map(() => 25), counts], rows: counts.length,
  } };
  const option = analysisOption({id:"h",title:"Heat",viz:"heatmap"}, result, theme) as any;
  const render = (count:number) => option.series[0].renderItem({}, {value:(i:number)=>[0,0,count,60000][i],coord:(v:number[])=>[v[0]/1000,20],size:()=>[60,20],visual:()=>seriesSlot(0,dark)});
  const colours = counts.slice(1).map(count => render(count).style.fill);
  expect(new Set(colours).size).toBe(5);
  const levels = colours.map(relativeLuminance);
  const surface = relativeLuminance(theme.surface);
  expect(levels.every((level:number,i:number)=>i===0 || Math.abs(level-surface)>Math.abs(levels[i-1]-surface))).toBe(true);
  expect(colours.at(-1)).toBe(dark ? "#7dd3fc" : "#1d4ed8");
  expect(render(1).style.stroke).toBeUndefined();
  expect(colours[0]).toBe(dark ? "#0f2a43" : "#dbeafe");
  expect(render(0)).toBeUndefined();
  expect(render(99999).style.fill).toBe(colours.at(-1));
  expect(option.visualMap.dimension).toBe(5);
  expect(option.visualMap.min).toBe(0);
  expect(option.visualMap.max).toBe(6);
  expect(option.series[0].data.every((d:any)=>d.value[2]>0)).toBe(true);
  expect(option.series[0].data[1].value[2]).toBe(10);
});

it.each([false,true])("a window of only singleton cells stays dim (%s)",dark=>{
 const theme=chartThemeFor(dark),result={id:"h",status:"ok" as const,elapsed_ms:1,frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[[0],[1]],rows:1}};
 const option=analysisOption({id:"h",title:"Heat",viz:"heatmap"},result,theme) as any;
 const cell=option.series[0].renderItem({}, {value:(i:number)=>[0,0,1,60000][i],coord:(v:number[])=>[v[0]/1000,20],size:()=>[60,20],visual:()=>seriesSlot(0,dark)});
 expect(cell.style.fill).toBe(dark ? "#0f2a43" : "#dbeafe");
});
