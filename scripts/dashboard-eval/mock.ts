import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { runEvaluation, type EvaluationConfig, type EditInput } from './main';
import { type Ledger } from './transport';
import { type Spec } from './score';

export const failures=['unsaved','invalid','unchecked','empty','duplicate_check','missing_terminal','truncated','duplicate_tool','error_mutation','usage_missing','usage_failure','wrong_title','neighbor_grid','metadata','field_array'] as const;
export type Failure=typeof failures[number];
const spec=():Spec=>({version:1,name:'Mock dashboard',description:'Deterministic fixture',time:{range:'1h'},variables:[],annotations:[],panels:[
  {id:'actual-latency',title:'Latency',viz:'stat',query:{from:'spans',measures:['p95(duration_ms)'],by:['service','namespace']},thresholds:[{value:100}],unit:'ms',width:4,height:2,grid:{x:0,y:0}},
  {id:'actual-text',title:'Explanation',viz:'text',description:'No errors were recorded in this fixture.',grid:{x:4,y:0}},
]});
export const mockEdits=():EditInput[]=>[
  {operation:'title',panel_index:0,value:'Latency p95'},
  {operation:'threshold',panel_index:0,value:[{value:200}]},
  {operation:'add',panel:{id:'added-panel',title:'Added fixture panel',viz:'stat',query:{from:'spans',measures:['count()']},grid:{x:8,y:0}}},
  {operation:'remove',panel_id:'added-panel'},
  {operation:'unit',panel_index:0,value:'s'},
];
export function startMock(failure?:Failure) {
  const boards=new Map<string,any>(),threads=new Map<string,any[]>(),statuses=new Map<string,string>();
  const stats={creates:0,edits:0,posts:0,status_reads:0,version_reads:0,queries:0};
  const server=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(req) {
    const path=new URL(req.url).pathname;
    if(req.headers.get('Fanout-Request')!=='1')return new Response(null,{status:403});
    if(path.startsWith('/api/agent/threads/'))return Response.json({messages:threads.get(path.split('/').at(-1)!)??[]});
    if(path.startsWith('/api/agent/runs/')&&req.method==='GET'){stats.status_reads++;return Response.json({status:statuses.get(path.split('/').at(-1)!)??'unknown'});}
    if(path.endsWith('/versions')) {stats.version_reads++;const id=path.split('/').at(-2)!;return Response.json({versions:Array.from({length:boards.get(id)?.version??0},(_,i)=>({version:i+1,author_kind:'agent'}))});}
    if(path==='/api/panels/query') {
      stats.queries++;const {dashboard}=await req.json();
      if(failure==='invalid')return Response.json({problems:[{path:'panels'}]},{status:400});
      if(failure==='unchecked')return Response.json({},{status:503});
      const results=dashboard.panels.map((p:any)=>({id:p.id,status:p.viz==='text'?'empty':'ok',diagnosis:p.viz==='text'&&failure!=='empty'?'No error events':undefined,frame:{rows:p.viz==='text'?0:2}}));
      if(failure==='duplicate_check')results.push({...results[0]});return Response.json({results});
    }
    if(path!=='/api/agent/runs'||req.method!=='POST')return new Response(null,{status:404});
    stats.posts++;const input=await req.json(),prompt=input.messages[0].content;
    const edit=threads.has(input.threadId);const history=threads.get(input.threadId)??[];threads.set(input.threadId,history);
    const events:any[]=[{type:'RUN_STARTED',runId:input.runId,threadId:input.threadId}];
    const emitCall=(id:string,name:string,result:any,isError=false)=>{
      const args=JSON.stringify({id:result?.dashboard?.id});
      events.push({type:'TOOL_CALL_START',toolCallId:id,toolCallName:name}, {type:'TOOL_CALL_ARGS',toolCallId:id,delta:args.slice(0,2)},{type:'TOOL_CALL_ARGS',toolCallId:id,delta:args.slice(2)},{type:'TOOL_CALL_END',toolCallId:id},{type:'TOOL_CALL_RESULT',toolCallId:id,content:JSON.stringify(result),isError});
      history.push({role:'tool',toolCallId:id,error:isError?'failed':''});
    };
    let record:any;
    if(edit) {
      stats.edits++;record=structuredClone(boards.get(history.find(m=>m.dashboard_id)?.dashboard_id));
      const raw=prompt.slice(prompt.indexOf('{'),prompt.indexOf('. Preserve'));
      const change=JSON.parse(raw);
      if(change.add_panel)record.spec.panels.push(change.add_panel);
      else if(change.remove_panel_id){record.spec.panels=record.spec.panels.filter((p:any)=>p.id!==change.remove_panel_id);record.spec.panels[1].grid={x:0,y:1};}
      else record.spec.panels.find((p:any)=>p.id===change.panel_id)[change.field]=change.value;
      record.version++;
      if(failure==='wrong_title'&&stats.edits===1)record.spec.panels[0].title='Wrong';
      if(failure==='neighbor_grid'&&stats.edits===1)record.spec.panels[1].grid.x=0;
      if(failure==='metadata'&&stats.edits===1)record.spec.time.range='2h';
      if(failure==='field_array'&&stats.edits===1)record.spec.panels[0].query.by.reverse();
    } else {stats.creates++;record={id:'board-'+stats.creates,version:1,spec:spec(),created_at:'fixture'};history.push({dashboard_id:record.id});}
    boards.set(record.id,structuredClone(record));
    if(failure!=='unsaved')emitCall(input.runId+'-save',edit?'edit_dashboard':'create_dashboard',{dashboard:record},failure==='error_mutation');
    if(failure==='duplicate_tool')events.push({type:'TOOL_CALL_START',toolCallId:input.runId+'-save',toolCallName:'create_dashboard'});
    // A later unrelated read must never replace the immutable save observation.
    emitCall(input.runId+'-read','get_dashboard',{dashboard:{...record,id:'unrelated',version:999}});
    if(failure!=='usage_missing')events.push({type:'CUSTOM',name:'model_call_usage',value:{run_id:input.runId,step:1,provider:'mock',model:'mock-no-provider',status:failure==='truncated'?'incomplete':failure==='usage_failure'?'error':'completed',usage:{input_tokens:2,output_tokens:1,cache_read_tokens:0,cache_write_tokens:0,reasoning_tokens:0}}});
    if(failure!=='missing_terminal')events.push(failure==='usage_failure'?{type:'RUN_ERROR',runId:input.runId}:{type:'RUN_FINISHED',runId:input.runId,truncated:failure==='truncated'});
    statuses.set(input.runId,failure==='truncated'?'truncated':failure==='missing_terminal'?'running':failure==='usage_failure'?'failed':'completed');
    const wire=events.map((e,i)=>i%2?`data: ${JSON.stringify(e)}\n\n`:`data: ${JSON.stringify(e)}\r\n\r\n`).join('');
    return new Response(new ReadableStream({start(c){const bytes=new TextEncoder().encode(wire);for(let i=0;i<bytes.length;i+=17)c.enqueue(bytes.slice(i,i+17));c.close();}}),{headers:{'Content-Type':'text/event-stream'}});
  }});
  return {server,base:`http://127.0.0.1:${server.port}`,stats};
}
export async function mockEvaluation(failure?:Failure) {
  const mock=startMock(failure);
  const ledger:Ledger={schema:1,rates:[{provider:'mock',model:'mock-no-provider',verified_at:'2026-10-08T00:00:00Z',input_includes_cache:true,input:0,output:0,cache_read:0,cache_write:0}],calls:[],prompts:[]};
  const hash=(value:string)=>createHash('sha256').update(value).digest('hex');
  const end=BigInt(Date.now())*1_000_000n;
  const snapshot={source_hash:hash(JSON.stringify(spec())),start_ns:String(end-3_600_000_000_000n),end_ns:String(end),shift_ns:'0',replayed_at:new Date().toISOString()};
  const config:EvaluationConfig={base:mock.base,cookies:'',model_label:'mock',prompts:Array.from({length:10},(_,i)=>({id:`mock-${i+1}`,prompt:`Simulate fixture ${i+1}.`})),edits:mockEdits(),ledger,cap_usd:0,mock:true,set:'benchmark',snapshot,snapshot_manifest_hash:hash(JSON.stringify(snapshot)),candidate_source_hash:hash('mock fixture candidate')};
  try {const result=await runEvaluation(config);return {...result,stats:mock.stats};}finally{mock.server.stop(true);}
}
export async function verifyMock():Promise<number> {
  const good=await mockEvaluation();assert.equal(good.exit_code,0);assert.equal(good.stats.creates,10);assert.equal(good.stats.edits,5);assert.equal(good.stats.posts,15);assert.equal(good.evidence.actual_cost_usd,0);
  for(const failure of failures){const bad=await mockEvaluation(failure);assert.notEqual(bad.exit_code,0,failure);if(['missing_terminal','truncated','duplicate_tool','usage_failure','usage_missing','unchecked','duplicate_check'].includes(failure)){assert.equal(bad.stats.posts,1);assert.equal(bad.stats.status_reads,1);}}
  return failures.length;
}
if(import.meta.main) {try{const n=await verifyMock();console.log(`MOCK PASS: ${n} failure injections; $0`);}catch{console.error('Mock verification failed');process.exitCode=1;}}
