import { escapeHTML } from "./escape";
import type { ChartSize, ChartTheme } from "./compile";
import type { Panel, PanelResult, VarValue } from "./types";

export type AnnotationBody = { from: string; to: string; services?: string[]; namespace?: string };
export type Deploy = { namespace: string; service: string; version: string; at: string };
export type Anomaly = { namespace: string; service: string; kind: string; from: string; to: string; title: string; severity: string };
export type AnnotationsResponse = { deploys: Deploy[]; anomalies: Anomaly[]; truncated?: boolean };

export function withAnnotations(option: Record<string, unknown>, panel: Panel, result: PanelResult, annotations: AnnotationsResponse, _vars: Record<string, VarValue>, theme: ChartTheme, size: ChartSize = { width: 500, height: 248 }): Record<string, unknown> {
  if (!["timeseries", "heatmap", "state_timeline"].includes(panel.viz)) return option;
  const scope = result.annotation_scope;
  if (result.annotation_error) return option;
  const matches = (a: { service: string; namespace: string }) => !scope || scope.services.some(s => s.service === a.service && (s.namespace === a.namespace || a.namespace === "" && !scope.namespace_scoped));
  const from = result.from_ms ?? -Infinity, to = result.to_ms ?? Infinity;
  const deploys = annotations.deploys.filter(matches).filter(a => Date.parse(a.at) >= from && Date.parse(a.at) < to).map(a => ({
    xAxis: Date.parse(a.at), name: `${a.service} ${a.version}`,
    label: { show: true, formatter: `${a.service} ${a.version}`, color: theme.muted, position: "end", distance: 0, offset: [4,-8], align: "left", verticalAlign: "bottom", rotate: 0, backgroundColor: theme.surface, padding: [2,4], borderRadius: 3, textBorderColor: theme.surface, textBorderWidth: 3, fontFamily: theme.font, fontSize: 12, overflow: "truncate", ellipsis: "…", width: Math.max(24, size.width - 32) },
    symbol: ["none", "path://M0,0 L0,-6"], symbolSize: [1,6],
    lineStyle: { type: "dashed", color: theme.muted, width: 1 },
    tooltip: { formatter: () => escapeHTML(`${a.service} · ${a.version} · ${a.at}`) },
  }));
  const episodes: { service: string; namespace: string; from: number; to: number; bad: boolean; titles: string[]; details: string[] }[] = [];
  for (const a of annotations.anomalies.filter(matches).filter(a => Date.parse(a.to) > from && Date.parse(a.from) < to)
    .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.service.localeCompare(b.service) || Date.parse(a.from) - Date.parse(b.from))) {
    const start = Math.max(from, Date.parse(a.from)), end = Math.min(to, Date.parse(a.to));
    const detail = escapeHTML(`${a.service} · ${a.title} · ${a.severity} · ${a.from} – ${a.to}`);
    const last = episodes.at(-1);
    if (last && last.service === a.service && last.namespace === a.namespace && start <= last.to) {
      last.to = Math.max(last.to, end); last.bad ||= a.severity === "bad";
      if (!last.titles.includes(a.title)) last.titles.push(a.title);
      if (!last.details.includes(detail)) last.details.push(detail);
    } else episodes.push({ service: a.service, namespace: a.namespace, from: start, to: end, bad: a.severity === "bad", titles: [a.title], details: [detail] });
  }
  const anomalies = episodes.map(a => [{
    xAxis: a.from, name: a.titles.join(" · "),
    itemStyle: { color: theme.status.warn, opacity: theme.dark ? .12 : .09 },
    label: { show: false, formatter: "anomaly", position: [0,-8], align: "left", verticalAlign: "bottom", width: 0, overflow: "truncate", backgroundColor: theme.surface, padding: [2,4], borderRadius: 3, color: theme.muted, textBorderColor: theme.surface, textBorderWidth: 3, fontFamily: theme.font, fontSize: 12 }, tooltip: { formatter: () => a.details.join("\n") },
  }, { xAxis: a.to }]);
  const series = (option.series ?? []) as Record<string, unknown>[];
  if (!series.length) return option;
  const grid = (option.grid ?? {}) as Record<string, unknown>;
  const yAxis = (Array.isArray(option.yAxis) ? option.yAxis[0] : option.yAxis) as { axisLabel?: { width?: number; margin?: number } } | undefined;
  // Use a conservative axis-label budget when grid containment moves the plot
  // inward. It prevents labels separated in nominal coordinates from colliding.
  const axisBudget = grid.containLabel ? (yAxis?.axisLabel?.width ?? 64) + (yAxis?.axisLabel?.margin ?? 8) : 0;
  const left = (typeof grid.left === "number" ? grid.left : 8) + axisBudget;
  const right = size.width - (typeof grid.right === "number" ? grid.right : 16);
  const laneWidth = Math.max(1, right - left);
  const times = deploys.map(d => d.xAxis);
  const windowFrom = Number.isFinite(from) ? from : Math.min(...times,...episodes.map(a=>a.from));
  const windowTo = Number.isFinite(to) ? to : Math.max(...times.map(t=>t+1),...episodes.map(a=>a.to));
  const measure = (text:string) => size.measureText?.(text,`12px ${theme.font}`) ?? text.length*12;
  anomalies.forEach((area,i)=>{
    const label=area[0].label!;
    const width=(episodes[i].to-episodes[i].from)/Math.max(1,windowTo-windowFrom)*laneWidth;
    label.width=Math.max(0,width-8);label.show=width>=measure("anomaly")+8;
    label.position=[0,deploys.length ? -34 : -8];
  });
  const clusters: { first: typeof deploys[number]; count: number; x: number; details: string[] }[] = [];
  for (const deploy of [...deploys].sort((a, b) => a.xAxis - b.xAxis)) {
    const x = left + (deploy.xAxis - windowFrom) / Math.max(1, windowTo - windowFrom) * laneWidth;
    const last = clusters.at(-1);
    if (last && x - last.x <= 12) { last.count++; last.details.push(deploy.tooltip.formatter()); }
    else clusters.push({ first: deploy, count: 1, x, details: [deploy.tooltip.formatter()] });
  }
  for (const cluster of clusters) {
    if (cluster.count > 1) cluster.first.label.formatter = `${cluster.count} deploys`;
    cluster.first.tooltip.formatter = () => cluster.details.join("<br/>");
  }
  clusters.forEach((cluster,i)=>{
    const label=cluster.first.label;
    const available=(clusters[i+1]?.x??right)-cluster.x-12;
    if(available<48 && i===clusters.length-1) {label.align="right";label.offset=[-4,-8];label.width=Math.max(24,cluster.x-left-12);}
    else label.width=Math.max(16,available);
  });
  const legend = option.legend as {show?:boolean;data?:string[];formatter?:(name:string)=>string;itemGap?:number} | undefined;
  let used = 0;
  for (const name of legend?.data ?? []) {
    const entry = 10 + 5 + measure(legend?.formatter?.(name) ?? name);
    if (used && used + (legend?.itemGap ?? 6) + entry > size.width-16) break;
    used += (used ? legend?.itemGap ?? 6 : 0) + entry;
  }
  const labels = [
    ...clusters.map(c=>({text:c.first.label.formatter,details:c.details.join("<br/>")})),
    ...anomalies.flatMap(a=>a[0].label?.show ? [{text:"anomaly",details:a[0].tooltip!.formatter()}] : []),
  ];
  const required = labels.reduce((n,l)=>n+measure(l.text)+12,0);
  const share = Boolean(legend?.show && required <= size.width-16-used-12);
  const top = (typeof grid.top === "number" ? grid.top : 8) + (labels.length && !share ? 18 : 0);
  const bandTop = share ? 1 : top-17;
  // Both marker types share one horizontal band. Lines/areas retain their exact
  // time coordinates and detailed tooltips; label placement follows free space.
  let rightOffset = 8;
  const chips = labels.map(l=>{
    const width = Math.max(16,Math.min(measure(l.text)+6,(size.width-16)/Math.max(1,labels.length)-6));
    const chip = {type:"text",right:rightOffset,top:bandTop,annotation:true,style:{text:l.text,fill:theme.muted,backgroundColor:theme.surface,padding:[1,3],fontSize:11,fontFamily:theme.font,width,overflow:"truncate",stroke:theme.surface,lineWidth:3},tooltip:{formatter:()=>l.details}};
    rightOffset += width+6; return chip;
  });
  for (const c of clusters) c.first.label.show=false;
  for (const a of anomalies) a[0].label!.show=false;
  const adaptAxis = (axis:Record<string,unknown>) => axis?.type === "value" || axis?.type === "log" ? {...axis,splitNumber:Math.max(2,Math.floor((size.height-top-Number(grid.bottom??8)-22)/32)),axisLabel:{...(axis.axisLabel as object),hideOverlap:true}} : axis;
  let axes = Array.isArray(option.yAxis) ? option.yAxis.map(adaptAxis) : option.yAxis ? adaptAxis(option.yAxis as Record<string,unknown>) : undefined;
  let trimmedSeries = series, graphics = [...(option.graphic as unknown[] ?? []),...chips];
  if(panel.viz === "state_timeline" && axes && !Array.isArray(axes)) {
    const names=axes.data as string[], capacity=Math.max(1,Math.floor((size.height-top-Number(grid.bottom??8)-22)/16));
    if(names.length>capacity) {
      axes={...axes,data:names.slice(0,capacity)};
      trimmedSeries=series.map(s=>({...s,data:(s.data as {value:number[]}[]).filter(d=>d.value[1]<capacity)}));
      const old=graphics.find(g=>(g as {style?:{text?:string}}).style?.text?.match(/^\+\d+ rows$/)) as {style:{text:string};tooltip:{formatter():string}} | undefined;
      const count=names.length-capacity+Number(old?.style.text.match(/\d+/)?.[0]??0);
      graphics=graphics.filter(g=>g!==old);
      graphics.push({type:"text",right:0,bottom:0,style:{text:`+${count} rows`,fill:theme.muted,fontSize:11,fontFamily:theme.font},tooltip:{formatter:()=>names.slice(capacity).map(escapeHTML).join("<br/>")+(old?"<br/>"+old.tooltip.formatter():"")}});
    }
  }
  return {
    ...option, ...(deploys.length || anomalies.length ? { tooltip: { ...((option.tooltip as Record<string, unknown>) ?? {}), renderMode: "html" } } : {}),
    grid:{...grid,top}, ...(axes ? {yAxis:axes} : {}),graphic:graphics,
    series: trimmedSeries.map((s, i) => {
      if (i !== 0) return s;
      const markLine = (s.markLine ?? {}) as { data?: unknown[] };
      const markArea = (s.markArea ?? {}) as { data?: unknown[] };
      return { ...s,
        markLine: { ...markLine, silent: false, symbol: ["none", "none"], data: [...(markLine.data ?? []), ...clusters.map(c => c.first)] },
        markArea: { ...markArea, silent: false, data: [...(markArea.data ?? []), ...anomalies] },
      };
    }),
  };
}
