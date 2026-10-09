import { withAnnotations } from "./annotations";
import { chartThemeFor } from "./compile";

it.each([false, true])("merges six adjacent windows and labels the anomaly row once with its source count (dark=%s)", dark => {
  const anomalies = Array.from({length:6},(_,i)=>({namespace:"shop",service:"cart",kind:"volume",title:"Changed",severity:"warn",from:new Date(i*1000).toISOString(),to:new Date((i+1)*1000).toISOString()}));
  const option=withAnnotations({series:[{type:"line"}]},{id:"p",title:"P",viz:"timeseries"},{id:"p",status:"ok",elapsed_ms:0,from_ms:0,to_ms:10000},{deploys:[],anomalies},{},chartThemeFor(dark)) as any;
  const areas=option.series[0].markArea.data;
  expect(areas).toHaveLength(1);expect(areas[0].map((a:any)=>a.xAxis)).toEqual([0,6000]);
  const labels=option.graphic.filter((g:any)=>g.annotation);
  expect(labels.map((g:any)=>g.style.text)).toEqual(["anomaly"]);
  expect(labels[0].tooltip.formatter()).toContain("6 windows");
  expect(areas[0][0].tooltip.formatter()).toContain("6 windows");
});

it("uses one anomaly label across six service scopes and drops colliding deploy labels without removing markers",()=>{
  const anomalies=Array.from({length:6},(_,i)=>({namespace:"shop",service:`svc-${i}`,kind:"latency",title:"Slow",severity:"bad",from:new Date(1000).toISOString(),to:new Date(9000).toISOString()}));
  const deploys=Array.from({length:6},(_,i)=>({namespace:"shop",service:`svc-${i}`,version:"v2",at:new Date(1000+i).toISOString()}));
  const option=withAnnotations({series:[{type:"line"}],grid:{left:8,right:8}},{id:"p",title:"P",viz:"timeseries"},{id:"p",status:"ok",elapsed_ms:0,from_ms:0,to_ms:10000},{deploys,anomalies},{},chartThemeFor(false),{width:360,height:248,measureText:text=>text.length*6}) as any;
  expect(option.graphic.filter((g:any)=>g.style.text==="anomaly")).toHaveLength(1);
  expect(option.series[0].markLine.data).toHaveLength(6);
  const labels=option.graphic.filter((g:any)=>g.annotation);
  expect(labels.some((g:any)=>g.style.text.includes("deploys"))).toBe(false);
  expect(labels.length).toBeLessThan(7);
  const boxes=labels.map((g:any)=>({right:g.right,left:g.right+g.style.width+6}));
  for(let i=1;i<boxes.length;i++)expect(boxes[i].right).toBeGreaterThanOrEqual(boxes[i-1].left);
  expect(boxes.at(-1).left).toBeLessThanOrEqual(360-8);
});
