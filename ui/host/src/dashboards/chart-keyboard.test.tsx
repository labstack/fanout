import { MantineProvider } from '@mantine/core';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect,it,vi } from 'vitest';
import { ChartKeyboard } from './chart-keyboard';
import { keyboardPoints } from '../../../panels/keyboard';
import { EChartCanvas } from './echart-canvas';
import { TimeseriesViz } from './viz/timeseries';
import { BarViz } from './viz/bar';
import { AnalysisChart } from './viz/analysis-chart';
import { pointSelection } from '../../../panels/interaction';
import { panelHandlers, drillSelection } from './panel-handlers';
import type { Panel, PanelResult } from '../../../panels/types';
import { FragmentView } from './fragment-view';
import { fixture } from '../../tests/fixtures';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TableViz } from './viz/table';
import { Waterfall } from './trace/trace-components';
import { traceFixture } from '../../tests/fixtures';

const chart=vi.hoisted(()=>({setOption:vi.fn(),dispatchAction:vi.fn(),dispose:vi.fn(),on:vi.fn(),resize:vi.fn()}));
vi.mock('echarts/core',()=>({init:()=>chart,use:()=>{},connect:()=>{},disconnect:()=>{}}));
vi.mock('echarts/charts',()=>({BarChart:{},LineChart:{},CustomChart:{},HeatmapChart:{},ScatterChart:{}}));
vi.mock('echarts/components',()=>({AriaComponent:{},BrushComponent:{},DataZoomComponent:{},GraphicComponent:{},GridComponent:{},LegendComponent:{},MarkAreaComponent:{},MarkLineComponent:{},ToolboxComponent:{},TooltipComponent:{},VisualMapComponent:{}}));
vi.mock('echarts/features',()=>({LabelLayout:{}}));
vi.mock('echarts/renderers',()=>({CanvasRenderer:{}}));
const panel:Panel={id:'p',title:'Latency',viz:'timeseries',unit:'ms',query:{from:'spans',measures:['count()'],by:['service']},drill:'traces',click:{set_variable:'service'}};
const result:PanelResult={id:'p',status:'ok',elapsed_ms:0,from_ms:1000,to_ms:5000,frame:{
  rows:4,columns:[{name:'time',type:'time',role:'time'},{name:'service',type:'string',role:'dimension'},{name:'count',type:'number',role:'measure',unit:'ms'}],
  values:[[2000,3000,2000,3000],['checkout · previous','checkout · previous','cart','cart'],[25,30,12,14]],
}};
const findButton=(el:ParentNode,text:string)=>[...el.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent===text)!;
async function press(el:HTMLElement,key:string,extra:KeyboardEventInit={}){await act(async()=>{el.focus();el.dispatchEvent(new KeyboardEvent('keydown',{key,bubbles:true,cancelable:true,...extra}));});}
async function input(el:HTMLInputElement,value:string){await act(async()=>{
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(el,value);
  el.dispatchEvent(new InputEvent('input',{bubbles:true,data:value,inputType:'insertText'}));
  el.dispatchEvent(new Event('change',{bubbles:true}));
});}

it('explores with one virtual point tab stop, moves with focus/key events, and invokes the existing callback once',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);
  const datum={value:[2000,25],selection:{dimensions:{service:'checkout'}}};
  const points=keyboardPoints([{name:'checkout',data:[datum,{value:[3000,30]}]}]);
  const click=vi.fn(),highlight=vi.fn();
  try{
    await act(async()=>root.render(<MantineProvider><ChartKeyboard points={points} label="Latency" summary={p=>`${p.event.seriesName} · ${p.event.value}`} onClick={click} onHighlight={highlight}/></MantineProvider>));
    expect(el.querySelectorAll('input')).toHaveLength(0);
    const explore=el.querySelector('button')!;
    await act(async()=>{explore.focus();explore.click();});
    const point=el.querySelector<HTMLElement>('[data-chart-point]')!;
    expect(document.activeElement).toBe(point);
    expect(el.querySelectorAll('[data-chart-point][tabindex="0"]')).toHaveLength(1);
    await act(async()=>point.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})));
    expect(click).toHaveBeenCalledExactlyOnceWith(points[0].event);
    await act(async()=>point.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true})));
    expect(highlight).toHaveBeenLastCalledWith(points[1]);
    expect(el.querySelector('[aria-live]')?.textContent).toContain('3000');
    document.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));
    expect(click).toHaveBeenCalledOnce();
  }finally{await act(async()=>root.unmount());el.remove();}
});

it.each([false,true])('uses real focus and ECharts highlight; Enter and mouse dispatch the identical Selection exactly once (dark=%s)',async dark=>{
  chart.on.mockClear();chart.dispatchAction.mockClear();
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);
  const selected=vi.fn(),variable=vi.fn();
  const handlers=panelHandlers(panel,result,variable,selected);
  try{
    await act(async()=>root.render(<MantineProvider forceColorScheme={dark?'dark':'light'}><TimeseriesViz panel={panel} result={result} dark={dark} height={240} {...handlers}/></MantineProvider>));
    await act(async()=>findButton(el,'Explore chart').click());
    const point=el.querySelector<HTMLElement>('[data-chart-point]')!;
    const live=el.querySelector('[aria-live]')!;
    expect(live.textContent).toContain('checkout · previous');
    expect(live.textContent).toContain('1970-01-01T00:00:02.000Z');
    expect(live.textContent).toContain('25.0ms');
    await press(point,'Enter');
    expect(selected).toHaveBeenCalledOnce();expect(variable).not.toHaveBeenCalled();
    const keyboard=selected.mock.calls[0][0];
    const mouse=chart.on.mock.calls.find(([name])=>name==='click')![1];
    mouse({seriesIndex:0,seriesName:'checkout · previous',value:[2000,25],data:[2000,25]});
    expect(selected).toHaveBeenCalledTimes(2);expect(selected.mock.lastCall![0]).toEqual(keyboard);
    expect(drillSelection(panel,result,keyboard,{namespace:'shop'})?.vars).toEqual({namespace:'shop',service:'checkout · previous'});
    expect(chart.dispatchAction).toHaveBeenCalledWith({type:'highlight',seriesIndex:0,dataIndex:0});
    const select=el.querySelector('select')!;
    await act(async()=>{select.value='1';select.dispatchEvent(new Event('change',{bubbles:true}));});
    await press(point,'Enter');expect(selected.mock.lastCall![0].dimensions).toEqual({service:'cart'});
    await input(el.querySelector('input[type="number"]')!,'2');
    await press(point,'Enter');expect(selected.mock.lastCall![0].time).toBe(3000);
    await act(async()=>findButton(el,'Close exploration').click());
    expect(chart.dispatchAction).toHaveBeenCalledWith({type:'downplay',seriesIndex:1,dataIndex:1});
    expect(chart.dispatchAction).toHaveBeenCalledWith({type:'hideTip'});
    expect(document.activeElement).toBe(findButton(el,'Explore chart'));
  }finally{await act(async()=>root.unmount());el.remove();}
});

it('extends and explicitly commits a UTC range once; rejects invalid ranges without local dataZoom',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);const zoom=vi.fn();
  chart.dispatchAction.mockClear();
  try{
    await act(async()=>root.render(<MantineProvider><TimeseriesViz panel={panel} result={result} dark={false} height={240} onZoom={zoom}/></MantineProvider>));
    await act(async()=>findButton(el,'Explore chart').click());
    const point=el.querySelector<HTMLElement>('[data-chart-point]')!;
    await press(point,'ArrowRight',{shiftKey:true});
    const start=el.querySelector<HTMLInputElement>('input[aria-label="Range start (UTC)"],input[id]')!;
    const fields=[...el.querySelectorAll<HTMLInputElement>('input')].filter(i=>i.type!=='number');
    expect(start).toBeTruthy();expect(fields.map(i=>i.value)).toEqual(['1970-01-01T00:00:02.000Z','1970-01-01T00:00:03.000Z']);
    expect(zoom).not.toHaveBeenCalled();
    await act(async()=>findButton(el,'Zoom to range').click());
    expect(zoom).toHaveBeenCalledExactlyOnceWith(2000,3000);
    for(const [from,to] of [['invalid','invalid'],['1970-01-01T00:00:03Z','1970-01-01T00:00:02Z'],['1970-01-01T00:00:00Z','1970-01-01T00:00:03Z'],['1970-01-01T00:00:02Z','1970-01-01T00:00:06Z'],['1970-01-01T00:00:02','1970-01-01T00:00:03']]){
      await input(fields[0],from);await input(fields[1],to);
      await act(async()=>findButton(el,'Zoom to range').click());expect(el.querySelector('[role="alert"]')).not.toBeNull();
    }
    expect(zoom).toHaveBeenCalledOnce();
    expect(chart.dispatchAction.mock.calls.some(([action])=>action.type==='dataZoom')).toBe(false);
  }finally{await act(async()=>root.unmount());el.remove();}
});

it('keeps the original range anchor across consecutive Shift arrows and contracts back toward it',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);
  const points=keyboardPoints([{name:'Requests',data:[[1000,1],[2000,2],[3000,3],[4000,4]]}]);
  try{
    await act(async()=>root.render(<MantineProvider><ChartKeyboard points={points} label="Requests" summary={p=>p.label} onHighlight={vi.fn()} onZoom={vi.fn()} bounds={{from:1000,to:5000}} pointWindow={p=>({from:Number((p.event.value as number[])[0]),to:Number((p.event.value as number[])[0])})}/></MantineProvider>));
    await act(async()=>findButton(el,'Explore chart').click());
    const point=el.querySelector<HTMLElement>('[data-chart-point]')!;
    await press(point,'ArrowRight',{shiftKey:true});await press(point,'ArrowRight',{shiftKey:true});
    const inputs=[...el.querySelectorAll<HTMLInputElement>('input')].filter(i=>i.type!=='number');
    expect(inputs.map(i=>i.value)).toEqual(['1970-01-01T00:00:01.000Z','1970-01-01T00:00:03.000Z']);
    await press(point,'ArrowLeft',{shiftKey:true});
    expect(inputs.map(i=>i.value)).toEqual(['1970-01-01T00:00:01.000Z','1970-01-01T00:00:02.000Z']);
  }finally{await act(async()=>root.unmount());el.remove();}
});

it('recomputes responsive displayed candidates, clamps selection without invoking actions and cleans up',async()=>{
  let resize!:ResizeObserverCallback;
  vi.stubGlobal('ResizeObserver',class{constructor(callback:ResizeObserverCallback){resize=callback;}observe(){}disconnect(){}});
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);
  const click=vi.fn(),option={series:[{name:'Visible',interactive:true,data:[1,2,3]}]},short={series:[{name:'Visible',interactive:true,data:[9]}]};
  try{
    const draw=(option:Record<string,unknown>)=>root.render(<MantineProvider><EChartCanvas option={option} optionForSize={()=>short} height={100} label="Displayed" onClick={click} keyboard={{}}/></MantineProvider>);
    await act(async()=>draw(option));await act(async()=>findButton(el,'Explore chart').click());
    const point=el.querySelector<HTMLElement>('[data-chart-point]')!;
    await press(point,'ArrowRight');await press(point,'ArrowRight');
    const canvas=el.querySelector<HTMLElement>('[role="img"]')!;
    Object.defineProperty(canvas,'clientWidth',{value:400});Object.defineProperty(canvas,'clientHeight',{value:100});
    await act(async()=>resize([],{} as ResizeObserver));
    expect(el.querySelector('input')?.value).toBe('1');expect(point.textContent).toContain('9');expect(click).not.toHaveBeenCalled();
    await act(async()=>draw({series:[]}));
    expect(point.textContent).toContain('9'); // responsive compile still owns the displayed data
    await act(async()=>root.unmount());expect(chart.dispose).toHaveBeenCalled();
  }finally{el.remove();vi.unstubAllGlobals();}
});

it.each(['bar','histogram','scatter','heatmap','state_timeline'] as const)('keyboard and mouse share custom/category selection for %s',async viz=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);const select=vi.fn();chart.on.mockClear();
  const columns=viz==='scatter'?[{name:'service',type:'string' as const,role:'dimension' as const},{name:'x',type:'number' as const,role:'measure' as const},{name:'y',type:'number' as const,role:'measure' as const}]:
    viz==='bar'?[{name:'service',type:'string' as const,role:'dimension' as const},{name:'count',type:'number' as const,role:'measure' as const}]:
    viz==='state_timeline'?result.frame!.columns:[{name:'time',type:'time' as const,role:'time' as const},{name:'bucket_lower',type:'number' as const,role:'dimension' as const},{name:'bucket_upper',type:'number' as const,role:'dimension' as const},{name:'count',type:'number' as const,role:'measure' as const}];
  const values=viz==='scatter'?[['checkout'],[3],[5]]:viz==='bar'?[['checkout'],[3]]:viz==='state_timeline'?[[2000],['checkout'],[3]]:[[2000],[0],[10],[3]];
  const p={...panel,viz};const r={...result,frame:{rows:1,columns,values}};
  const Chart=viz==='bar'?BarViz:AnalysisChart;
  try{
    await act(async()=>root.render(<MantineProvider><Chart panel={p} result={r} height={240} dark={false} onPoint={select}/></MantineProvider>));
    await act(async()=>findButton(el,'Explore chart').click());const point=el.querySelector<HTMLElement>('[data-chart-point]')!;
    await press(point,'Enter');expect(select).toHaveBeenCalledOnce();
    const series=(chart.setOption.mock.lastCall![0].series as {data:unknown[];name:string}[])[0];
    const data=series.data[0];const value=typeof data==='object'?(data as {value:unknown}).value:data;
    chart.on.mock.calls.find(([name])=>name==='click')![1]({seriesIndex:0,seriesName:series.name,name:viz==='bar'?'checkout':undefined,value,data});
    expect(select).toHaveBeenCalledTimes(2);expect(select.mock.lastCall![0]).toEqual(select.mock.calls[0][0]);
  }finally{await act(async()=>root.unmount());el.remove();}
});

it('never offers previous/helper/Other/null or unsupported custom selections',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);const select=vi.fn();
  const series=[{name:'Other',interactive:true,data:[[2000,3]]},{name:'checkout · previous',interactive:false,data:[[2000,3]]},{name:'helper',interactive:false,data:[3]},{name:'empty',interactive:true,data:[[2000,null]]},{name:'empty bar',interactive:true,data:[{value:null,selection:{dimensions:{service:'cart'}}}]}];
  try{
    await act(async()=>root.render(<MantineProvider><EChartCanvas option={{series}} label="Rejected" height={100} onClick={select} keyboard={{canSelect:event=>Boolean(pointSelection(panel,result,event))}}/></MantineProvider>));
    expect(findButton(el,'Explore chart').disabled).toBe(true);expect(select).not.toHaveBeenCalled();
    await act(async()=>root.render(<MantineProvider><AnalysisChart panel={{...panel,viz:'scatter'}} result={{...result,frame:{rows:0,values:[],columns:[]}}} height={100} dark={false} onPoint={select}/></MantineProvider>));
    expect(findButton(el,'Explore chart').disabled).toBe(true);expect(select).not.toHaveBeenCalled();
  }finally{await act(async()=>root.unmount());el.remove();}
});

it('applies a click-only bar filter once through the shared panel-handler precedence',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);
  const p={...panel,viz:'bar' as const,drill:undefined};
  const r={...result,frame:{rows:1,columns:[{name:'service',type:'string' as const,role:'dimension' as const},{name:'count',type:'number' as const,role:'measure' as const}],values:[['checkout'],[25]]}};
  const variable=vi.fn(),point=vi.fn();const handlers=panelHandlers(p,r,variable,point);
  try{
    await act(async()=>root.render(<MantineProvider><BarViz panel={p} result={r} height={100} dark={false} {...handlers}/></MantineProvider>));
    await act(async()=>findButton(el,'Explore chart').click());
    await press(el.querySelector<HTMLElement>('[data-chart-point]')!,'Enter');
    expect(variable).toHaveBeenCalledExactlyOnceWith('service','checkout');
    expect(point).toHaveBeenCalledExactlyOnceWith({dimensions:{service:'checkout'}});
  }finally{await act(async()=>root.unmount());el.remove();}
});

it('chat renders the same keyboard point/drill path and sends one scoped variable batch',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);const client=new QueryClient();
  const f=fixture();f.dashboard.panels=[panel];f.results=[result];f.dashboard.variables=[{name:'service',kind:'text'}];
  const query=vi.fn().mockResolvedValue(f),exemplars=vi.fn().mockResolvedValue({traces:[]}),trace=vi.fn();
  try{
    await act(async()=>root.render(<MantineProvider><QueryClientProvider client={client}><FragmentView fragment={f} dark={false} onQuery={query} drillClient={{exemplars,trace}}/></QueryClientProvider></MantineProvider>));
    await act(async()=>findButton(el,'Explore chart').click());
    for(const key of ['r','e','h'])await press(el.querySelector<HTMLElement>('[data-chart-point]')!,key);
    expect(query).not.toHaveBeenCalled();expect(document.querySelector('[role="dialog"]')).toBeNull();
    await press(el.querySelector<HTMLElement>('[data-chart-point]')!,'Enter');
    expect(query).toHaveBeenCalledOnce();expect(query.mock.lastCall![0].vars).toEqual({service:'checkout · previous'});
    expect(query.mock.lastCall![0].time).toEqual(f.dashboard.time);
    expect(exemplars.mock.lastCall![0]).toMatchObject({dimensions:{service:'checkout · previous'}});
  }finally{await act(async()=>root.unmount());el.remove();client.clear();}
});

it('chat keyboard range commits one complete batch, preserving comparison and the reset window',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);const client=new QueryClient();
  const f=fixture();f.dashboard.panels=[panel];f.results=[result];f.dashboard.time.compare='previous_period';f.vars={service:'checkout'};
  const query=vi.fn().mockResolvedValue(f);
  try{
    await act(async()=>root.render(<MantineProvider><QueryClientProvider client={client}><FragmentView fragment={f} dark={false} onQuery={query} drillClient={{exemplars:vi.fn(),trace:vi.fn()}}/></QueryClientProvider></MantineProvider>));
    await act(async()=>findButton(el,'Explore chart').click());
    await press(el.querySelector<HTMLElement>('[data-chart-point]')!,'ArrowRight',{shiftKey:true});
    await act(async()=>findButton(el,'Zoom to range').click());
    expect(query).toHaveBeenCalledOnce();
    expect(query.mock.lastCall![0]).toMatchObject({time:{from:'1970-01-01T00:00:02.000Z',to:'1970-01-01T00:00:03.000Z',compare:'previous_period',refresh:'off'}});
    expect(query.mock.lastCall![0]).not.toHaveProperty('panels');
    expect(query.mock.lastCall![0].dashboard.panels).toEqual([panel]);
    await act(async()=>el.querySelector<HTMLButtonElement>('[aria-label="Reset Latency zoom"]')!.click());
    expect(query).toHaveBeenCalledTimes(2);expect(query.mock.lastCall![0].time).toEqual(f.dashboard.time);
  }finally{await act(async()=>root.unmount());el.remove();client.clear();}
});

it('retains existing row and waterfall Enter activation',async()=>{
  const el=document.createElement('div');document.body.append(el);const root=createRoot(el);const selected=vi.fn();
  const r={...result,frame:{rows:1,columns:[{name:'service',type:'string' as const,role:'dimension' as const}],values:[['checkout']]}};
  try{
    await act(async()=>root.render(<MantineProvider><TableViz panel={{...panel,viz:'table'}} result={r} height={200} onPoint={selected}/></MantineProvider>));
    const row=el.querySelector<HTMLElement>('tbody tr')!;
    await press(row,'Enter');expect(selected).toHaveBeenCalledExactlyOnceWith({dimensions:{service:'checkout'}});
    const span=vi.fn();
    await act(async()=>root.render(<MantineProvider><Waterfall spans={traceFixture.data.spans} dark={false} onSpan={span}/></MantineProvider>));
    await press(el.querySelector<HTMLElement>('tbody tr')!,'Enter');
    expect(span).toHaveBeenCalledExactlyOnceWith(traceFixture.data.spans[0]);
  }finally{await act(async()=>root.unmount());el.remove();}
});
