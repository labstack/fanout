import { expect, it } from 'vitest';
import { keyboardPoints } from '../../../panels/keyboard';
import { pointSelection } from '../../../panels/interaction';
import { analysisOption } from '../../../panels/analysis';
import { chartThemeFor, timeseriesOption } from '../../../panels/compile';
import type { Panel, PanelResult } from '../../../panels/types';
it('preserves compiled data identity and displayed order',()=>{
  const datum={value:[10,25],selection:{dimensions:{service:'checkout'}}};
  const points=keyboardPoints([{name:'checkout',data:[null,datum]},{name:'empty',data:[]}]);
  expect(points).toHaveLength(1);expect(points[0].series_index).toBe(0);expect(points[0].data_index).toBe(1);
  expect(points[0].event.data).toBe(datum);
});

const panel: Panel = {id:'p',title:'P',viz:'timeseries',query:{from:'spans',measures:['count()'],by:['service']}};
const result: PanelResult = {id:'p',status:'ok',elapsed_ms:0,from_ms:1000,to_ms:5000};
it('uses identical current/previous/Other rejection for mouse and keyboard, preserving legitimate names',()=>{
  for(const [name,interactive,accepted] of [['checkout',true,true],['checkout · previous',true,true],['checkout · previous',false,false],['Other',true,false]] as const){
    const series={name,interactive,data:[[2000,3]]};
    const mouse={seriesName:name,interactive,value:series.data[0],data:series.data[0]};
    const point=keyboardPoints([{...series,interactive:true}])[0];
    point.event.interactive=interactive;
    expect(pointSelection(panel,result,mouse)).toEqual(pointSelection(panel,result,point.event));
    expect(Boolean(pointSelection(panel,result,mouse))).toBe(accepted);
    if(!interactive)expect(keyboardPoints([series])).toEqual([]);
  }
});
it('excludes helpers and retains only compiled current series',()=>{
  const frame={rows:2,columns:[{name:'time',type:'number' as const,role:'time' as const},{name:'service',type:'string' as const,role:'dimension' as const},{name:'count',type:'number' as const,role:'measure' as const}],values:[[1000,2000],['checkout · previous','checkout · previous'],[1,2]]};
  const compiled=timeseriesOption(panel,{...result,frame,previous:frame,shift_ms:1000},chartThemeFor(false));
  const series=compiled.series as Parameters<typeof keyboardPoints>[0];
  expect(series.map(s=>s.interactive)).toEqual([true,false]);
  expect(keyboardPoints(series)).toHaveLength(2);
  for(const point of keyboardPoints(series))expect(pointSelection(panel,result,point.event)?.dimensions.service).toBe('checkout · previous');
});
it('keeps real scatter rows selectable in an Other group while rejecting synthetic Other dimensions',()=>{
  const p:Panel={...panel,viz:'scatter',query:{from:'spans',by:['endpoint','service'],measures:['count()','p95(duration_ms)']}};
  const names=['a','b','c','d','e','f','g','h'];
  const r={...result,frame:{rows:8,columns:[{name:'endpoint',type:'string' as const,role:'dimension' as const},{name:'service',type:'string' as const,role:'dimension' as const},{name:'count',type:'number' as const,role:'measure' as const},{name:'p95',type:'number' as const,role:'measure' as const}],values:[names.map(n=>'/'+n),names,names.map(()=>1),names.map(()=>2)]}};
  const compiled=analysisOption(p,r,chartThemeFor(false));
  const points=keyboardPoints(compiled.series as Parameters<typeof keyboardPoints>[0]);
  const point=points.find(p=>p.event.seriesName?.startsWith('Other'))!;
  expect(point).toBeDefined();expect(pointSelection(p,r,point.event)).toEqual((point.event.data as {selection:unknown}).selection);
  expect(pointSelection({...p,viz:'histogram'},r,{seriesName:'Other (2)',interactive:true,data:{selection:{dimensions:{service:'Other (2)'}}}})).toBeUndefined();
});
it.each(['heatmap','histogram','scatter','state_timeline'] as const)('preserves compiled custom %s selections',viz=>{
  const columns=viz==='scatter'?[{name:'service',type:'string' as const,role:'dimension' as const},{name:'x',type:'number' as const,role:'measure' as const},{name:'y',type:'number' as const,role:'measure' as const}]:
    viz==='state_timeline'?[{name:'time',type:'number' as const,role:'time' as const},{name:'service',type:'string' as const,role:'dimension' as const},{name:'count',type:'number' as const,role:'measure' as const}]:
      [{name:'time',type:'number' as const,role:'time' as const},{name:'bucket_lower',type:'number' as const,role:'dimension' as const},{name:'bucket_upper',type:'number' as const,role:'dimension' as const},{name:'count',type:'number' as const,role:'measure' as const}];
  const values=viz==='scatter'?[['checkout'],[3],[5]]:viz==='state_timeline'?[[1000],['checkout'],[3]]:[[1000],[0],[10],[3]];
  const p={...panel,viz};const r={...result,frame:{rows:1,columns,values}};
  const compiled=analysisOption(p,r,chartThemeFor(false));
  const points=keyboardPoints(compiled.series as Parameters<typeof keyboardPoints>[0]);
  expect(points).toHaveLength(1);
  const selection=(points[0].event.data as {selection:unknown}).selection;
  expect(pointSelection(p,r,points[0].event)).toBe(selection);
});
