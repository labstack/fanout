import { afterEach, expect, it } from 'bun:test';
import { mkdtempSync, mkdirSync, realpathSync, rmSync, symlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { readFileSync } from 'node:fs';
import { readSSE, requestJSON, cookieHeader, safeOutput, canStartPrompt, recordUsage, beginPrompt, settlePrompt, manualSettlement, findSaved, mutationEvidence, executePanels, type Ledger } from './transport';
import { mutatingTools } from './tool-catalog';
it('matches the server registered mutating tools exactly',()=>{
 const server=JSON.parse(readFileSync(join(import.meta.dir,'testdata/server.json'),'utf8'));
 expect([...mutatingTools].sort()).toEqual(server.mutating_tools);
});
const servers: ReturnType<typeof Bun.serve>[]=[];
const roots:string[]=[];
afterEach(()=>{servers.splice(0).forEach(s=>s.stop(true));roots.splice(0).forEach(r=>rmSync(r,{recursive:true,force:true}));});
function server(fetch:(req:Request)=>Response|Promise<Response>) {const s=Bun.serve({hostname:'127.0.0.1',port:0,fetch});servers.push(s);return `http://127.0.0.1:${s.port}`;}
const frame=(e:any)=>`data: ${JSON.stringify(e)}\r\n\r\n`;
const response=(s:string,split=1)=>new Response(new ReadableStream({start(c){const b=new TextEncoder().encode(s);for(let i=0;i<b.length;i+=split)c.enqueue(b.slice(i,i+split));c.close();}}),{headers:{'Content-Type':'text/event-stream'}});
const finish={type:'RUN_FINISHED',outcome:{type:'success'}};
const start={type:'TOOL_CALL_START',toolCallId:'save',toolCallName:'create_dashboard'};
const saved={id:'board',version:1,spec:{panels:[{id:'a'}]}};
it('keeps the executor error in a completed failing panel check', async () => {
  const message = 'Out of Memory Error: ' + 'details '.repeat(100) + 'final diagnostic';
  const spec = {panels:[{id:'p'}]};
  const got = await executePanels(async () => ({results:[
    {id:'p',status:'error',elapsed_ms:12,error:message},
  ]}), spec, new AbortController().signal);
  expect(got.checked).toBe(true);
  expect(got.valid).toBe(true);
  expect(got.checks[0]).toEqual({id:'p',status:'error',rows:0,error:message});
});
it('does not invent an executor error for successful panel checks', async () => {
  for (const error of [undefined, null, 42, {message:'not a wire string'}]) {
    const got = await executePanels(async () => ({results:[
      {id:'p',status:'ok',frame:{rows:1},error},
    ]}), {panels:[{id:'p'}]}, new AbortController().signal);
    expect(got).toEqual({checked:true,valid:true,checks:[{id:'p',status:'ok',rows:1}]});
  }
});
const result={type:'TOOL_CALL_RESULT',toolCallId:'save',content:JSON.stringify({dashboard:saved}),isError:false};
it('decodes split UTF-8, multiline frames, assistant call names and immutable saves',async()=>{
  const s=await readSSE(response(frame({type:'TEXT_MESSAGE_START',messageId:'text',role:'assistant'})+': ping\r\nevent: TEXT_MESSAGE_CONTENT\r\ndata: {"messageId":"text","delta":\r\ndata: "✓"}\r\n\r\n'+frame(start)+frame({type:'TOOL_CALL_END',toolCallId:'save'})+frame(result)+frame({type:'TOOL_CALL_START',toolCallId:'read',toolCallName:'get_dashboard'})+frame({type:'TOOL_CALL_RESULT',toolCallId:'read',content:JSON.stringify({dashboard:{...saved,id:'unrelated',version:9}})})+frame(finish)),new AbortController().signal);
  expect(s.incomplete).toBe(false);expect(s.tools[0].name).toBe('create_dashboard');expect(findSaved(s)?.record).toEqual(saved);
});
it('rejects duplicate tool IDs and error mutations',async()=>{
  expect((await readSSE(response(frame(start)+frame(start)+frame(result)+frame(finish)),new AbortController().signal)).incomplete).toBe(true);
  const s=await readSSE(response(frame(start)+frame({...result,isError:true})+frame(finish)),new AbortController().signal);expect(findSaved(s)).toBeNull();
  const unknown=await readSSE(response(frame(start)+frame({...result,isError:undefined})+frame(finish)),new AbortController().signal);expect(findSaved(unknown)).toBeNull();
  expect(findSaved(unknown,[{role:'tool',toolCallId:'save',error:'failed'}])).toBeNull();
});
it('uses the final committed restore after an earlier edit',async()=>{
 const restored={...saved,version:3};
 const s=await readSSE(response(frame({...start,toolCallName:'edit_dashboard'})+frame(result)+frame({type:'TOOL_CALL_START',toolCallId:'restore',toolCallName:'restore_dashboard_version'})+frame({type:'TOOL_CALL_RESULT',toolCallId:'restore',content:JSON.stringify({dashboard:restored}),isError:false})+frame(finish)),new AbortController().signal);
 expect(findSaved(s)?.record).toEqual(restored);expect(findSaved(s)?.tool_id).toBe('restore');
});
it('records missing terminal, truncation, RUN_ERROR and socket failure as incomplete',async()=>{
  const g=JSON.parse(readFileSync(join(import.meta.dir,'testdata/server.json'),'utf8'));
  for(const raw of [frame(start),g.incomplete_sse,g.error_sse]) {
    expect((await readSSE(response(raw),new AbortController().signal)).incomplete).toBe(true);
  }
  const s=await readSSE(new Response(new ReadableStream({start(c){c.enqueue(new TextEncoder().encode(frame(start)));c.error(new Error('secret socket body'));}}),{headers:{'Content-Type':'text/event-stream'}}),new AbortController().signal);
  expect(s.incomplete).toBe(true);expect(JSON.stringify(s)).not.toContain('secret socket body');
});
it('aborts a pending stream without waiting for another byte',async()=>{
  const controller=new AbortController();const r=new Response(new ReadableStream({start(){},cancel(){}}),{headers:{'Content-Type':'text/event-stream'}});
  const promise=readSSE(r,controller.signal);controller.abort();expect((await promise).incomplete).toBe(true);
});
it('retries GET 503 once before consumption and never retries POST',async()=>{
  let gets=0,posts=0;
  const base=server(req=>req.method==='GET'? (++gets===1?new Response('private',{status:503}):Response.json({ok:true})):(posts++,new Response('private',{status:503})));
  expect(await requestJSON(base,'/api/read',{})).toEqual({ok:true});expect(gets).toBe(2);
  await expect(requestJSON(base,'/api/mutate',{method:'POST'})).rejects.toThrow('HTTP 503');expect(posts).toBe(1);
  let broken=0;const other=server(()=>{broken++;return new Response('not JSON')});await expect(requestJSON(other,'/api/read',{})).rejects.toThrow();expect(broken).toBe(1);
  let exhausted=0;const down=server(()=>{exhausted++;return new Response(null,{status:503})});await expect(requestJSON(down,'/api/read',{})).rejects.toThrow('HTTP 503');expect(exhausted).toBe(2);
});
it('does not retry an aborted GET',async()=>{
  let requests=0;const base=server(async()=>{requests++;await Bun.sleep(20);return Response.json({})});
  await expect(requestJSON(base,'/api/read',{signal:AbortSignal.timeout(5)})).rejects.toThrow();expect(requests).toBeLessThanOrEqual(1);
});
it('never follows redirects or accepts embedded credentials or cross-origin paths',async()=>{
  let n=0;const base=server(()=>{n++;return new Response(null,{status:302,headers:{Location:'/other'}})});
  await expect(requestJSON(base,'/api/read',{})).rejects.toThrow();expect(n).toBe(1);
  await expect(requestJSON('http://user:secret@127.0.0.1','/api/read',{})).rejects.toThrow();
  await expect(requestJSON(base,'//external.invalid',{})).rejects.toThrow();
});
it('applies cookie expiry, domain, secure and path rules without exposing secrets',()=>{
  const jar=['#HttpOnly_.example.test\tTRUE\t/api\tTRUE\t0\tsession\tprivate','.example.test\tTRUE\t/\tFALSE\t1\texpired\told','example.test\tFALSE\t/\tFALSE\t0\thost\tonly','.example.test\tTRUE\t/api2\tFALSE\t0\tother\tno'].join('\n');
  expect(cookieHeader(jar,new URL('https://sub.example.test/api/x'))).toBe('session=private');
  expect(cookieHeader(jar,new URL('http://sub.example.test/api/x'))).toBe('');
  expect(cookieHeader(jar,new URL('https://sub.example.test/apix'))).toBe('');
  expect(cookieHeader(jar,new URL('https://evil.test/api/x'))).toBe('');
});
it('refuses collisions, unsafe labels, symlink ancestors and internal sealed-set output',()=>{
  const root=mkdtempSync(join(realpathSync(tmpdir()),'fanout-eval-'));roots.push(root);mkdirSync(join(root,'.superpowers/eval'),{recursive:true});
  const out=join(root,'.superpowers/eval/run');expect(safeOutput(out,'benchmark',root)).toBe(out);
  expect(()=>safeOutput(out,'benchmark',root)).toThrow();
  symlinkSync(join(root,'.superpowers/eval'),join(root,'link'));expect(()=>safeOutput(join(root,'link/run2'),'benchmark',root)).toThrow();
  expect(()=>safeOutput(join(root,'.superpowers/eval/sealed-run'),'holdout',root,root)).toThrow();
});
const ledger=():Ledger=>({schema:1,rates:[{provider:'fake',model:'observed',verified_at:'2026-10-08T00:00:00Z',input_includes_cache:true,input:1,output:2,cache_read:0.1,cache_write:1.5}],calls:[],prompts:[]});
const call=(run_id:string,step=1)=>({run_id,step,provider:'fake',model:'observed',status:'completed',usage:{input_tokens:100000,output_tokens:10000,cache_read_tokens:10000,cache_write_tokens:0,reasoning_tokens:5000}});
it('uses the exact $0.60 prior before any measured prompt',()=>{
  const l=ledger();
  expect(canStartPrompt(l,0.60)).toBe(true);expect(canStartPrompt(l,0.599999)).toBe(false);
  expect(canStartPrompt(l,0)).toBe(false);
  for(const cap of [NaN,Infinity,-1])expect(canStartPrompt(l,cap)).toBe(false);
});
it('validates manual settlement inputs and permits reconciled missing usage',()=>{
  for(const [cost,reason] of [[NaN,'reason'],[Infinity,'reason'],[-1,'reason'],[0,' '],[0,'x'.repeat(1001)]] as const){
    const l=ledger();beginPrompt(l,'run');expect(()=>manualSettlement(l,'run',cost,reason)).toThrow();expect(l.prompts[0].settled).toBe(false);
  }
  const l=ledger();beginPrompt(l,'run');recordUsage(l,{...call('run'),usage:null});
  expect(()=>manualSettlement(l,'unknown',1,'reconciled')).toThrow();
  manualSettlement(l,'run',1,'Controller reconciled usage');expect(canStartPrompt(l,1.60)).toBe(true);
  expect(()=>manualSettlement(l,'run',1,'already settled')).toThrow();
  const known=ledger();beginPrompt(known,'run');recordUsage(known,call('run'));
  expect(()=>manualSettlement(known,'run',0,'cannot erase spend')).toThrow();
  const ambiguous=ledger();ambiguous.prompts=['a','b'].map(run_id=>({run_id,prompt_id:'shared',settled:false,completed:false,cost_usd:null}));
  expect(()=>manualSettlement(ambiguous,'shared',0,'ambiguous')).toThrow();
});
it('debits each provider call once including failure and does not charge reasoning twice',()=>{
  const l=ledger();beginPrompt(l,'run');recordUsage(l,call('run'));expect(l.calls[0].cost_usd).toBeCloseTo(0.111);
  recordUsage(l,call('run'));expect(l.calls).toHaveLength(1);
  recordUsage(l,{...call('run',2),status:'error'});expect(l.calls).toHaveLength(2);expect(l.calls[1].cost_usd).toBeCloseTo(0.111);
  expect(canStartPrompt(l,100)).toBe(false);expect(settlePrompt(l,'run',false)).toBe(true);expect(canStartPrompt(l,100)).toBe(true);
});
it('keeps measured usage after a socket drop unsettled until reconciliation',async()=>{
  const l=ledger();beginPrompt(l,'run');let sent=false;
  const stream=new ReadableStream<Uint8Array>({pull(c){if(!sent){sent=true;c.enqueue(new TextEncoder().encode(frame({type:'CUSTOM',name:'model_call_usage',value:call('run')})));}else c.error(new Error('socket dropped'));}});
  const state=await readSSE(new Response(stream,{headers:{'Content-Type':'text/event-stream'}}),new AbortController().signal,u=>recordUsage(l,u));
  expect(state.usage).toHaveLength(1);expect(state.terminal).toBeNull();expect(state.incomplete).toBe(true);
  expect(settlePrompt(l,'run',false,Boolean(state.terminal||state.truncated))).toBe(false);
  expect(l.prompts[0].cost_usd).toBeCloseTo(0.111);expect(canStartPrompt(l,100)).toBe(false);
});
it('uses nearest-rank p95, equality at cap and accumulated ledger across invocations',()=>{
  const l=ledger();for(let i=1;i<=20;i++){beginPrompt(l,String(i));recordUsage(l,{...call(String(i)),usage:{...call('x').usage,input_tokens:i*100000,output_tokens:0,cache_read_tokens:0,reasoning_tokens:0}});expect(settlePrompt(l,String(i),true)).toBe(true);}
  const loaded=JSON.parse(JSON.stringify(l));const spent=loaded.calls.reduce((n:number,c:any)=>n+c.cost_usd,0);
  expect(canStartPrompt(loaded,spent+1.9)).toBe(true);expect(canStartPrompt(loaded,spent+1.9-0.00001)).toBe(false);
});
it('blocks missing usage, unsettled prompts, conflicting correlation and over-16 steps',()=>{
  const l=ledger();beginPrompt(l,'run');recordUsage(l,{...call('run'),usage:null});expect(settlePrompt(l,'run',true)).toBe(false);expect(canStartPrompt(l,100)).toBe(false);
  const other=ledger();beginPrompt(other,'run');expect(()=>recordUsage(other,call('run',17))).toThrow();recordUsage(other,call('run'));expect(()=>recordUsage(other,{...call('run'),usage:{...call('run').usage,input_tokens:1}})).toThrow();
  const missing=ledger();missing.rates=[];expect(canStartPrompt(missing,100)).toBe(false);
  const partial=ledger();beginPrompt(partial,'run');expect(()=>recordUsage(partial,{...call('run'),usage:{input_tokens:1} as any})).toThrow();
});

it('collects only the final assistant message by its stream ID',async()=>{
 const events=[{type:'TEXT_MESSAGE_START',messageId:'earlier',role:'assistant'},
 {type:'TEXT_MESSAGE_CONTENT',messageId:'earlier',delta:'Earlier'},
 {type:'TEXT_MESSAGE_START',messageId:'user',role:'user'},
 {type:'TEXT_MESSAGE_CONTENT',messageId:'user',delta:'Ignore'},
 {type:'TEXT_MESSAGE_START',messageId:'final',role:'assistant'},
 {type:'TEXT_MESSAGE_CONTENT',messageId:'final',delta:'Final ✓'},finish];
 const s=await readSSE(response(events.map(frame).join('')),new AbortController().signal);
 expect(s.final_text).toBe('Final ✓');expect(s.incomplete).toBe(false);
});
it('preserves nested tool errors and persisted flags without allowing saves or answers',async()=>{
 for(const content of [{error:{code:'tool_failed',message:'Synthetic invalid spec'}},
 {error:{code:'tool_failed',message:'Synthetic operation failed'}},{dashboard:saved}]) {
  const s=await readSSE(response(frame(start)+frame({...result,content:JSON.stringify(content),isError:true})+frame(finish)),new AbortController().signal);
  expect(findSaved(s,[{role:'tool',toolCallId:'save',error:false}])).toBeNull();
  expect(s.tools[0].is_error).toBe(true);
 }
 const s=await readSSE(response(frame(start)+frame(result)+frame(finish)),new AbortController().signal);
 expect(findSaved(s,[{role:'tool',toolCallId:'save',isError:true}])).toBeNull();
});

it('reads stable tool error codes and messages while preserving error flags from all sources',async()=>{
 for(const message of ['Synthetic invalid spec','Synthetic operation failed']) {
  const s=await readSSE(response(frame(start)+frame({...result,content:JSON.stringify({dashboard:saved,error:{code:'tool_failed',message}}),isError:false})+frame({type:'ACTIVITY_SNAPSHOT',activityType:'mcp-app',content:{tool_name:'create_dashboard',tool_input:null,is_error:false}})+frame(finish)),new AbortController().signal);
  expect(findSaved(s,[{role:'tool',toolCallId:'save',error:false}])).toBeNull();
  expect(s.tools[0]).toMatchObject({is_error:true,error_code:'tool_failed',error_message:message});
 }
});

it('does not infer a successful mutation from a missing or malformed persisted error flag',async()=>{
 for(const flag of [{},{error:null},{isError:'false'}]) {
  const s=await readSSE(response(frame(start)+frame({...result,isError:undefined})+frame(finish)),new AbortController().signal);
  expect(findSaved(s,[{role:'tool',toolCallId:'save',...flag}])).toBeNull();
 }
});
it('does not certify answer mutation evidence when persisted tool errors or results are unknown',async()=>{
 const s=await readSSE(response(frame({...start,toolCallName:'get_telemetry_schema'})+frame({...result,content:JSON.stringify({services:[]})})+frame(finish)),new AbortController().signal);
 const messages=[{role:'tool',toolCallId:'save',error:{code:'tool_failed',message:'Synthetic operation failed'}}];
 findSaved(s,messages);expect(mutationEvidence(s,messages).mutation_evidence_complete).toBe(false);
});

it('marks assistant content without a started message ID as incomplete',async()=>{
 const s=await readSSE(response(frame({type:'TEXT_MESSAGE_CONTENT',delta:'Synthetic prose'})+frame(finish)),new AbortController().signal);
 expect(s.incomplete).toBe(true);expect(s.final_text).toBe('');
});

it('never clears an earlier activity error or conflicting protocol error flag',async()=>{
 for(const prefix of [frame({type:'ACTIVITY_SNAPSHOT',activityType:'mcp-app',content:{tool_name:'create_dashboard',tool_input:null,is_error:true}}),'']) {
  const s=await readSSE(response(frame(start)+prefix+frame({...result,...(prefix?{}:{is_error:true})})+frame(finish)),new AbortController().signal);
  expect(findSaved(s,[{role:'tool',toolCallId:'save',error:false}])).toBeNull();
 }
});
