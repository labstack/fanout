import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { runEvaluation, type EvaluationConfig, type EditInput } from './main';
import {startJudgeMock} from './judge-mock';
import {MODELS} from './judge';
import { type Ledger } from './transport';
import { type Spec, normalizeAddedPanel, score } from './score';

export const failures=['unsaved','invalid','unchecked','empty','duplicate_check','missing_terminal','truncated','duplicate_tool','error_mutation','usage_missing','usage_failure','wrong_title','neighbor_grid','metadata','field_array','slow','model_mismatch','mixed_models','record_metadata','version_jump','different_dashboard','unauthorized','rate_limited'] as const;
export type Failure=typeof failures[number]|'configuration_missing'|'configuration_changed'|'last_truncated'|'multiple_saves'|'edit_model_mismatch'|'dated_model'|'iso_dated_model'|'wrong_suffix'|'invalid_date'|'answer_create'|'answer_replace'|'answer_restore'|'two_boards'|'thread_failure'|'blank_answer';
const golden=()=>JSON.parse(readFileSync(join(import.meta.dir,'testdata/server.json'),'utf8'));
const spec=():Spec=>structuredClone(golden().saved_spec);
const wireEvents=(wire:string):any[]=>wire.split('\n').filter(s=>s.startsWith('data: ')).map(s=>JSON.parse(s.slice(6)));
export const mockEdits=():EditInput[]=>[
  {operation:'title',panel_index:0,value:'Latency p95'},
  {operation:'threshold',panel_index:0,value:[{value:200,status:'warn'}]},
  {operation:'add',panel:golden().add_panel,after:'actual_latency'},
  {operation:'remove',panel_id:'added_panel'},
  {operation:'unit',panel_index:0,value:'s'},
];
export function startMock(failure?:Failure,intents?:EvaluationConfig['prompts']) {
  const g=golden(),templates=wireEvents(g.incomplete_sse),errorEvents=wireEvents(g.error_sse);
  const boards=new Map<string,any>(),threads=new Map<string,any[]>(),statuses=new Map<string,string>();
  const stats={creates:0,edits:0,posts:0,status_reads:0,version_reads:0,queries:0};
  const server=Bun.serve({hostname:'127.0.0.1',port:0,async fetch(req) {
    const path=new URL(req.url).pathname;
    if(req.headers.get('Fanout-Request')!=='1')return new Response(null,{status:403});
    if(path.startsWith('/api/agent/threads/')&&failure==='thread_failure')return Response.json({code:'unavailable',message:'Synthetic unavailable thread'},{status:503});
    if(path.startsWith('/api/agent/threads/'))return Response.json({id:path.split('/').at(-1)!,messages:threads.get(path.split('/').at(-1)!)??[],updated_at:'2026-10-01 13:00:00'});
    if(path.startsWith('/api/agent/runs/')&&req.method==='GET'){stats.status_reads++;return Response.json({status:statuses.get(path.split('/').at(-1)!)??'unknown'});}
    if(path.endsWith('/versions')) {stats.version_reads++;const id=path.split('/').at(-2)!;return Response.json({versions:Array.from({length:boards.get(id)?.version??0},(_,i)=>({version:i+1,author_kind:'agent'}))});}
    if(path==='/api/panels/query') {
      stats.queries++;const {dashboard}=await req.json();
      if(failure==='invalid')return Response.json({code:'invalid_dashboard',message:'Synthetic validation',problems:[{path:'panels'}]},{status:400});
      if(failure==='unchecked')return Response.json({},{status:503});
      const results=dashboard.panels.map((p:any)=>({...structuredClone(p.viz==='text'?g.results.find((r:any)=>r.id==='actual_text'):p.viz==='timeseries'?g.added_results.find((r:any)=>r.id==='added_panel'):g.results.find((r:any)=>r.id==='actual_latency')),id:p.id}));
      if(failure==='empty'){results[0].status='empty';delete results[0].frame;delete results[0].diagnosis;}
      if(failure==='duplicate_check')results.push({...results[0]});return Response.json({results});
    }
    if(path!=='/api/agent/runs'||req.method!=='POST')return new Response(null,{status:404});
    stats.posts++;if(failure==='unauthorized'||failure==='rate_limited')return new Response(null,{status:failure==='unauthorized'?401:429});const input=await req.json(),prompt=input.messages[0].content;
    const answer=intents?.find(p=>p.prompt===prompt)?.expect==='answer'&&!['answer_create','answer_replace','answer_restore','two_boards'].includes(failure??'');
    const edit=threads.has(input.threadId);const history=threads.get(input.threadId)??[];threads.set(input.threadId,history);
    const events:any[]=[{...structuredClone(templates.find(e=>e.type==='RUN_STARTED')),runId:input.runId,threadId:input.threadId},{...structuredClone(templates.find(e=>e.name==='model_configuration')),value:{provider:'mock',model:'mock-no-provider'}}];
    if(failure==='configuration_missing')events.splice(1,1);
    if(failure==='configuration_changed'&&stats.posts===10)events[1].value.model='changed-default';
    const emitCall=(id:string,name:string,result:any,isError=false)=>{
      const args=JSON.stringify({id:result?.dashboard?.id});
      const template=(type:string)=>({...structuredClone(templates.find(e=>e.type===type)),toolCallId:id});
      events.push({...template('TOOL_CALL_START'),toolCallName:name},{...template('TOOL_CALL_ARGS'),delta:args.slice(0,2)},{...template('TOOL_CALL_ARGS'),delta:args.slice(2)},template('TOOL_CALL_END'),{...template('TOOL_CALL_RESULT'),content:JSON.stringify(result)});
      history.push({id:`tool-${id}`,role:'tool',toolCallId:id,content:JSON.stringify(result),...(isError?{error:'failed'}:{})});
    };
    let record:any;
    if(edit) {
      stats.edits++;record=structuredClone(boards.get(history.find(m=>m.dashboard_id)?.dashboard_id));
      const raw=prompt.slice(prompt.indexOf('{'),prompt.indexOf('. Preserve'));
      const change=JSON.parse(raw);
      if(change.add_panel){const at=change.after?record.spec.panels.findIndex((p:any)=>p.id===change.after)+1:record.spec.panels.length;const added=normalizeAddedPanel(change.add_panel);added.grid=structuredClone(g.added_spec.panels.find((p:any)=>p.id===added.id).grid);record.spec.panels.splice(at,0,added);}
      else if(change.remove_panel_id){record.spec.panels=record.spec.panels.filter((p:any)=>p.id!==change.remove_panel_id);record.spec.panels[1].grid={x:0,y:1};}
      else record.spec.panels.find((p:any)=>p.id===change.panel_id)[change.field]=change.value;
      record.version++;
      if(failure==='wrong_title'&&stats.edits===1)record.spec.panels[0].title='Wrong';
      if(failure==='neighbor_grid'&&stats.edits===1)record.spec.panels[1].grid.x=0;
      if(failure==='metadata'&&stats.edits===1)record.spec.time.range='2h';
      if(failure==='field_array'&&stats.edits===1)record.spec.panels[0].query.where.reverse();
    if(failure==='record_metadata'&&stats.edits===1)record.name='Changed record';
      if(failure==='version_jump'&&stats.edits===1)record.version++;
      if(failure==='different_dashboard'&&stats.edits===1)record.id='different-board';
    } else if(!answer) {stats.creates++;record={...structuredClone(g.saved_record),id:'board-'+stats.creates,spec:spec()};history.push({dashboard_id:record.id});}else record={...structuredClone(g.saved_record),spec:spec()};
    if(!answer)boards.set(record.id,structuredClone(record));
    if(failure!=='unsaved'&&!answer)emitCall(input.runId+'-save',edit?'edit_dashboard':failure==='answer_replace'?'replace_dashboard':failure==='answer_restore'?'restore_dashboard_version':'create_dashboard',{dashboard:record},failure==='error_mutation');
    if(failure==='two_boards')emitCall(input.runId+'-other','create_dashboard',{dashboard:{...record,id:'other-board'}});
    if(failure==='multiple_saves'&&!edit){record.version=2;record.spec.description='Corrected fixture context.';boards.set(record.id,structuredClone(record));emitCall(input.runId+'-correction','edit_dashboard',{dashboard:record});}
    if(failure==='duplicate_tool')events.push({type:'TOOL_CALL_START',toolCallId:input.runId+'-save',toolCallName:'create_dashboard'});
    // A later unrelated read must never replace the immutable save observation.
    emitCall(input.runId+'-read','get_dashboard',{dashboard:{...record,id:'unrelated',version:999}});
    events.push({type:'TEXT_MESSAGE_START',messageId:'final',role:'assistant'},{type:'TEXT_MESSAGE_CONTENT',messageId:'final',delta:failure==='blank_answer'?' ':answer?'Synthetic percentile explanation.':'Synthetic saved dashboard.'},{type:'TEXT_MESSAGE_END',messageId:'final'});
    if(failure!=='usage_missing'){
      const metered=structuredClone(templates.filter(e=>e.name==='model_call_usage').at(-1));
      metered.value={...metered.value,run_id:input.runId,step:1,provider:'mock',model:failure==='model_mismatch'||failure==='mixed_models'&&stats.posts===10||failure==='edit_model_mismatch'&&stats.edits===2?'different-model':'mock-no-provider',status:failure==='truncated'||failure==='last_truncated'&&stats.posts===10?'incomplete':failure==='usage_failure'?'error':'completed'};
      if(failure==='dated_model')metered.value.model+='-20261008';
      if(failure==='iso_dated_model')metered.value.model+='-2026-10-08';
      if(failure==='wrong_suffix')metered.value.model+='-latest';
      if(failure==='invalid_date')metered.value.model+='-20261399';
      events.push(metered);
    }
    if(failure!=='missing_terminal')events.push({...structuredClone(failure==='usage_failure'?errorEvents.find(e=>e.type==='RUN_ERROR'):templates.find(e=>e.type==='RUN_FINISHED')),runId:input.runId,threadId:input.threadId});
    statuses.set(input.runId,failure==='truncated'?'truncated':failure==='missing_terminal'?'running':failure==='usage_failure'?'failed':'completed');
    const wire=events.map((e,i)=>i%2?`data: ${JSON.stringify(e)}\n\n`:`data: ${JSON.stringify(e)}\r\n\r\n`).join('');
    return new Response(new ReadableStream({async start(c){const cut=failure==='multiple_saves'&&!edit?wire.indexOf('\n\n',wire.indexOf('TOOL_CALL_RESULT'))+2:wire.length;const send=(text:string)=>{const bytes=new TextEncoder().encode(text);for(let i=0;i<bytes.length;i+=17)c.enqueue(bytes.slice(i,i+17));};send(wire.slice(0,cut));if(cut<wire.length){await Bun.sleep(5);send(wire.slice(cut));}c.close();}}),{headers:{'Content-Type':'text/event-stream'}});
  }});
  return {server,base:`http://127.0.0.1:${server.port}`,stats};
}
export async function mockEvaluation(failure?:Failure,options:Partial<Pick<EvaluationConfig,'model_label'|'ledger'|'mock'|'cap_usd'|'prompts'|'set'|'edits'>>&{judge?:boolean}={}) {
  const mock=startMock(failure,options.prompts);
  const judgeMock=options.judge?startJudgeMock():null;
  const ledger:Ledger={schema:1,rates:[{provider:'mock',model:'mock-no-provider',verified_at:'2026-10-08T00:00:00Z',input_includes_cache:true,input:0,output:0,cache_read:0,cache_write:0},{provider:'mock',model:'different-model',verified_at:'2026-10-08T00:00:00Z',input_includes_cache:true,input:0,output:0,cache_read:0,cache_write:0}],calls:[],prompts:[]};
  if(judgeMock)for(const [provider,model] of Object.entries(MODELS))ledger.rates.push({...ledger.rates[0],provider,model,input_includes_cache:provider==='openai'});
  for(const suffix of ['20261008','2026-10-08','latest','20261399'])ledger.rates.push({...ledger.rates[0],model:'mock-no-provider-'+suffix});
  const hash=(value:string)=>createHash('sha256').update(value).digest('hex');
  const end=BigInt(Date.now())*1_000_000n;
  const snapshot={source_hash:hash(JSON.stringify(spec())),start_ns:String(end-3_600_000_000_000n),end_ns:String(end),shift_ns:'0',replayed_at:new Date().toISOString()};
  const config:EvaluationConfig={base:mock.base,cookies:'',model_label:'mock:mock-no-provider',prompts:Array.from({length:10},(_,i)=>({id:`mock-${i+1}`,prompt:`Simulate fixture ${i+1}.`,expect:'dashboard' as const,rationale:'Synthetic benchmark'})),edits:mockEdits(),ledger,cap_usd:0,mock:true,set:'benchmark',snapshot,snapshot_manifest_hash:hash(JSON.stringify(snapshot)),candidate_source_hash:hash('mock fixture candidate'),...options,judge:judgeMock?{env:{JUDGE_ANTHROPIC_KEY:'mock-only-anthropic',JUDGE_OPENAI_KEY:'mock-only-openai'},endpoints:{anthropic:judgeMock.base,openai:judgeMock.base},fetch,mock:true}:undefined};
  try {const result=await runEvaluation(config);
    // Inject a measured latency at the scorer boundary; no product clock override.
    if(failure==='slow'){for(const r of result.evidence.runs)r.elapsed_ms=45_001;result.evidence.score=score(result.evidence.runs,result.evidence.edits);result.exit_code=1;}
    return {...result,stats:mock.stats};}finally{mock.server.stop(true);judgeMock?.server.stop(true);}
}
export async function verifyMock():Promise<number> {
  const good=await mockEvaluation();assert.equal(good.exit_code,0);assert.equal(good.stats.creates,10);assert.equal(good.stats.edits,5);assert.equal(good.stats.posts,15);assert.equal(good.evidence.actual_cost_usd,0);
  const measured:Partial<Record<Failure,string>>={unsaved:'s1',invalid:'s3',empty:'s2',error_mutation:'s1',truncated:'s1',usage_failure:'s1',wrong_title:'s5',neighbor_grid:'s5',metadata:'s5',field_array:'s5',slow:'s4',record_metadata:'s5',version_jump:'s5',different_dashboard:'s5'};
  for(const failure of failures){
    const bad=await mockEvaluation(failure),flag=measured[failure];
    assert.equal(bad.exit_code,flag?1:2,failure);
    if(flag)assert.equal(bad.evidence.score[flag],false,failure);
    if(['missing_terminal','duplicate_tool','usage_missing','unchecked','duplicate_check','unauthorized','rate_limited'].includes(failure)){assert.equal(bad.stats.posts,1,failure);assert.equal(bad.stats.status_reads,1,failure);}
    if(['truncated','usage_failure'].includes(failure)){assert.equal(bad.stats.posts,10,failure);assert.equal(bad.evidence.cost_complete,true,failure);}
    if(['model_mismatch','mixed_models'].includes(failure)){assert.ok(bad.evidence.model_mismatch.length,failure);for(const s of ['s1','s2','s3','s4','s5'])assert.equal(bad.evidence.score[s],false,failure);}
  }
  return failures.length;
}
if(import.meta.main) {try{const n=await verifyMock();console.log(`MOCK PASS: ${n} failure injections; $0`);}catch{console.error('Mock verification failed');process.exitCode=1;}}
