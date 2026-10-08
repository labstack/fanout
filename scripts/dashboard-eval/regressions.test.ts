import { expect, it, afterEach, spyOn } from 'bun:test';
import { readFileSync, mkdirSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { resolve, join } from 'node:path';
import * as cli from './main';
import * as transport from './transport';
import { score, compareEdit, panelPass } from './score';
import { mockEvaluation, mockEdits, startMock } from './mock';
const golden=()=>JSON.parse(readFileSync(join(import.meta.dir,'testdata/server.json'),'utf8'));
const roots:string[]=[];
afterEach(()=>roots.splice(0).forEach(r=>rmSync(r,{recursive:true,force:true})));
const ledger=():transport.Ledger=>({schema:1,rates:[{provider:'mock',model:'mock-no-provider',verified_at:'2026-10-08T00:00:00Z',input_includes_cache:true,input:1,output:1,cache_read:0,cache_write:0}],calls:[],prompts:[]});
const usage=(id:string,status='completed')=>({run_id:id,step:1,provider:'mock',model:'mock-no-provider',status,usage:{input_tokens:100000,output_tokens:0,cache_read_tokens:0,cache_write_tokens:0,reasoning_tokens:0}});
it('preserves structured save receipts from the Go golden generator',()=>{
 const g=golden(),out=g.saved_output,r=out.receipt;
 expect(out.dashboard).toEqual(g.saved_record);
 expect(r).toMatchObject({base_version:0,version:1,layout_changed:false,save_check:{checked:true,elapsed_ms:0}});
 expect(r.dashboard_fields).toBeUndefined();
 expect(r.changes.map((p:any)=>({panel_id:p.panel_id,kind:p.kind}))).toEqual(g.saved_spec.panels.map((p:any)=>({panel_id:p.id,kind:'added'})));
 expect(r.save_check.panels.map((p:any)=>p.id)).toEqual(g.saved_spec.panels.map((p:any)=>p.id));
 expect(r.save_check.panels.every((p:any)=>p.status==='ok'&&p.rows===1&&p.elapsed_ms===0)).toBe(true);
});
it('keeps real edit receipts for authored fields, order and packed layout',()=>{
 const g=golden(),out=g.edited_output;
 expect(out).toBeDefined();
 expect(out.receipt).toMatchObject({base_version:3,version:4,layout_changed:true,save_check:{checked:true,elapsed_ms:0}});
 expect(out.receipt.changes).toEqual([
  {panel_id:'actual_latency',title:'Updated latency',kind:'changed',fields:['title'],position_changed:true},
  {panel_id:'added_stat',title:'Added stat',kind:'removed'},
 ]);
 expect(out.receipt.save_check.panels.map((p:any)=>p.id)).toEqual(out.dashboard.spec.panels.map((p:any)=>p.id));
});
it('scores executor text results without inventing a frame',()=>{
 const g=golden(),p=g.saved_spec.panels.find((p:any)=>p.viz==='text'),r=g.results.find((r:any)=>r.id===p.id);
 expect(r.status).toBe('ok');expect(r.frame).toBeUndefined();expect(panelPass(p,[{...r,rows:0}])).toBe(true);
 expect(panelPass({...p,content:' '},[{...r,rows:0}])).toBe(false);
});
it('accepts only omitted server defaults on an inserted panel and honours after',()=>{
 const g=golden();expect(g.add_panel.query.bucket).toBeUndefined();const built=cli.buildEdit(g.saved_spec,{operation:'add',panel:g.add_panel,after:g.saved_spec.panels[0].id} as any);
 expect(built.expected.panels[1].id).toBe(g.add_panel.id);expect(built.prompt).toContain('after');
 expect(compareEdit(g.saved_spec,g.added_spec,built.expected,'add').passed).toBe(true);
 for(const mutate of [(s:any)=>s.panels[1].title='wrong',(s:any)=>s.panels[0].width=12,(s:any)=>s.panels[1].unknown='extra',(s:any)=>s.panels[1].query.bucket='5m',(s:any)=>s.panels.reverse()]){
  const bad=structuredClone(g.added_spec);mutate(bad);expect(compareEdit(g.saved_spec,bad,built.expected,'add').passed).toBe(false);
 }
 const explicit=cli.buildEdit(g.saved_spec,{operation:'add',panel:{...g.add_panel,width:12},after:g.saved_spec.panels[0].id} as any);
 expect(compareEdit(g.saved_spec,g.added_spec,explicit.expected,'add').passed).toBe(false);
});
it('reads real incomplete usage and run errors from the server encoder',async()=>{
 for(const wire of [golden().incomplete_sse,golden().error_sse]){
  const state=await transport.readSSE(new Response(wire,{headers:{'Content-Type':'text/event-stream'}}),new AbortController().signal);
  expect(state.incomplete).toBe(true);
  if(state.terminal==='RUN_FINISHED')expect(state.truncated).toBe(true);
 }
 const invented=await transport.readSSE(new Response('data: {"type":"RUN_FINISHED","truncated":true}\n\n',{headers:{'Content-Type':'text/event-stream'}}),new AbortController().signal);
 expect(invented.truncated).toBe(false);
});
it('times the last scored save and preserves first-save timing',async()=>{
 const state=await transport.readSSE(new Response('data: {"type":"RUN_FINISHED"}\n\n',{headers:{'Content-Type':'text/event-stream'}}),new AbortController().signal);
 state.tools=[18_000,70_000].map((elapsed_ms,i)=>({id:String(i),name:i?'edit_dashboard':'create_dashboard',args:'{}',ended:true,is_error:false,elapsed_ms,result:{dashboard:{id:'board',version:i+1,spec:{panels:[]}}}}));
 expect(transport.findSaved(state)).toMatchObject({elapsed_ms:70_000,first_elapsed_ms:18_000,record:{version:2}});
});
it('requires complete runs and counts only checked validation failures',()=>{
 const runs=Array.from({length:10},()=>({complete:true,saved:true,elapsed_ms:10,checked:true,valid:true,panels:[{id:'a'}],checks:[{id:'a',status:'ok',rows:1}]}));
 runs[9].complete=false;expect(score(runs,[])).toMatchObject({s1:false,s4:false});
 Object.assign(runs[0],{valid:false,checked:false});expect(score(runs,[]).validation_failures).toBe(0);
 runs[0].checked=true;expect(score(runs,[]).validation_failures).toBe(1);
});
it('automatically settles metered failures without using them for p95',()=>{
 for(const status of ['error','incomplete']){
  const l=ledger();transport.beginPrompt(l,'failed');transport.recordUsage(l,usage('failed',status));
  expect(transport.settlePrompt(l,'failed',false)).toBe(true);expect(l.prompts[0]).toMatchObject({settled:true,completed:false,status:'failed'});
  expect(transport.spentUSD(l)).toBeCloseTo(.1);expect(transport.canStartPrompt(l,.7)).toBe(true);expect(transport.canStartPrompt(l,.699)).toBe(false);
 }
});
it('manually settles unknown usage without deleting metered spend across invocations',async()=>{
 const root=resolve('.superpowers/eval/settlement-'+crypto.randomUUID());roots.push(root);mkdirSync(root,{recursive:true});
 const l=ledger();transport.beginPrompt(l,'unknown');transport.recordUsage(l,usage('unknown'));transport.recordUsage(l,{...usage('unknown'),step:2,usage:null});
 expect(transport.settlePrompt(l,'unknown',false)).toBe(false);const path=join(root,'ledger.json');writeFileSync(path,JSON.stringify(l));
 expect(await cli.main(['--settle','unknown','--cost-usd','0.25','--reason','Controller verified usage','--cost-ledger',path],()=>{})).toBe(0);
 const loaded=JSON.parse(readFileSync(path,'utf8'));expect(loaded.calls).toHaveLength(2);expect(loaded.prompts[0].manual_settlement.reason).toBe('Controller verified usage');
 expect(transport.spentUSD(loaded)).toBeCloseTo(.25);expect(transport.canStartPrompt(loaded,.85)).toBe(true);expect(transport.canStartPrompt(loaded,.849)).toBe(false);
 expect(await cli.main(['--settle','unknown','--cost-usd','0','--reason','erase','--cost-ledger',path],()=>{})).toBe(2);
});
it('blocks unlabeled mixed and mismatched models and records the mismatch',async()=>{
 for(const failure of ['model_mismatch','mixed_models']){
  const r=await mockEvaluation(failure as any);expect(r.exit_code).toBe(2);expect(r.evidence.model_mismatch).toBeTruthy();
  for(const flag of ['s1','s2','s3','s4','s5'])expect(r.evidence.score[flag]).toBe(false);
 }
});
it('refuses nonexistent format fields and stale replay manifests',()=>{
 expect(()=>cli.buildEdit({panels:[{id:'a'}]},{operation:'unit',panel_id:'a',field:'format',value:'ms'})).toThrow();
 expect(()=>(cli as any).validateSnapshot({source_hash:'0'.repeat(64),start_ns:'-2',end_ns:'-1',shift_ns:'0',replayed_at:new Date().toISOString()})).toThrow();
 expect(()=>(cli as any).assertPromptHash('benchmark','a74ce656679ca792'+'0'.repeat(49))).toThrow();
});
it('refuses controller-only runs before output creation and gives safe categories',async()=>{
 const root=resolve('.superpowers/eval/refusal-'+crypto.randomUUID());roots.push(root);mkdirSync(root,{recursive:true});
 const file=(n:string,v:any)=>{const p=join(root,n);writeFileSync(p,JSON.stringify(v));return p;};
 const args=['--base','http://127.0.0.1:1','--cookies',file('cookies.txt',''),'--out',join(root,'out'),'--prompts-file',file('prompts.json',[]),'--set','holdout','--budget-usd','1','--cost-ledger',file('ledger.json',ledger()),'--snapshot-manifest',file('snapshot.json',{}),'--edits-file',file('edits.json',[])];
 const lines:string[]=[];
 expect(await cli.main(args,x=>lines.push(x))).toBe(2);expect(existsSync(join(root,'out'))).toBe(false);expect(lines.at(-1)).toContain('sealed_hash');
 expect(await cli.main([...args,'--holdout-sha','0'.repeat(64)],x=>lines.push(x))).toBe(2);expect(lines.at(-1)).toContain('output_root');expect(existsSync(join(root,'out'))).toBe(false);
});
it('uses separate bounded timers for post-stream evidence',async()=>{
 const timers:number[]=[],original=AbortSignal.timeout.bind(AbortSignal),spy=spyOn(AbortSignal,'timeout').mockImplementation((ms:number)=>{timers.push(ms);return original(ms)});
 try{expect((await mockEvaluation()).exit_code).toBe(0);expect(timers.filter(ms=>ms===30_000).length).toBe(30);}finally{spy.mockRestore();}
});
it('does not write mock artifacts when CI opts out',async()=>{
 const out=resolve('.superpowers/eval/no-output-'+crypto.randomUUID());roots.push(out);
 expect(await cli.main(['--mock','--no-output','--out',out],()=>{})).toBe(0);expect(existsSync(out)).toBe(false);
});

it('uses the configured default when no model label is supplied',async()=>{
 const good=await (mockEvaluation as any)(undefined,{model_label:undefined});expect(good.exit_code).toBe(0);expect(good.evidence.model_label).toBeNull();expect(good.evidence.default_model).toBe('mock:mock-no-provider');
 for(const failure of ['configuration_missing','configuration_changed']){
  const bad=await (mockEvaluation as any)(failure,{model_label:undefined});expect(bad.exit_code).toBe(2);expect(bad.evidence.model_mismatch.length).toBeGreaterThan(0);
 }
});
it('continues metered failed prompts only while the real spend gate permits',async()=>{
 const l=ledger();const r=await (mockEvaluation as any)('usage_failure',{ledger:l,mock:false,cap_usd:.600008});
 expect(r.exit_code).toBe(2);expect(r.stats.posts).toBe(3);expect(r.evidence.metered_cost_usd).toBeCloseTo(.000009,8);
 expect(l.prompts).toHaveLength(3);expect(l.prompts.every(p=>p.settled&&!p.completed)).toBe(true);expect(r.evidence.runs[3].error_code).toBe('budget_or_unsettled_usage');
});
it('does not pass save or latency evidence when only the tenth prompt truncates',async()=>{
 const r=await mockEvaluation('last_truncated' as any);expect(r.exit_code).toBe(1);expect(r.evidence.runs).toHaveLength(10);
 expect(r.evidence.runs[9]).toMatchObject({saved:true,complete:false,settlement_status:'failed'});expect(r.evidence.score).toMatchObject({s1:false,s4:false});
});
it('records first save and run end alongside the last immutable version',async()=>{
 const r=await mockEvaluation('multiple_saves' as any);expect(r.exit_code).toBe(0);
 for(const run of r.evidence.runs){expect(run.saved_record.version).toBe(2);expect(run.elapsed_ms).toBeGreaterThan(run.first_save_ms);expect(run.run_end_ms).toBeGreaterThanOrEqual(run.elapsed_ms);}
});
it('retains unverified metered calls until controller settlement',()=>{
 const l=ledger();transport.beginPrompt(l,'unknown');expect(()=>transport.recordUsage(l,{...usage('unknown'),model:'unverified'})).not.toThrow();
 expect(l.calls).toHaveLength(1);expect(l.calls[0].cost_usd).toBeNull();expect(transport.settlePrompt(l,'unknown',false)).toBe(false);expect(transport.canStartPrompt(l,10)).toBe(false);
});
it('enforces replay age independently of signed bounds with a five-minute tolerance',()=>{
 const now=Date.parse('2026-10-08T12:00:00Z'),s={source_hash:'0'.repeat(64),start_ns:String(BigInt(now-1000)*1_000_000n),end_ns:String(BigInt(now)*1_000_000n),shift_ns:'-1',replayed_at:new Date(now-300_000).toISOString()};
 expect(()=>cli.validateSnapshot(s,now)).not.toThrow();expect(()=>cli.validateSnapshot({...s,replayed_at:new Date(now-300_001).toISOString()},now)).toThrow();
 expect(()=>cli.validateSnapshot({...s,start_ns:'-2',end_ns:'-1'},now)).toThrow();
 expect(()=>cli.assertPromptHash('benchmark','a74ce656679ca792'+'0'.repeat(49))).toThrow();expect(()=>cli.assertPromptHash('benchmark','0'.repeat(64))).not.toThrow();
});
it('does not create controller output on a frozen hash mismatch',async()=>{
 const root=resolve('/private/tmp/eval-refusal-'+crypto.randomUUID());roots.push(root);mkdirSync(root,{recursive:true});
 const previous=process.env.FANOUT_HOLDOUT_OUT_ROOT;process.env.FANOUT_HOLDOUT_OUT_ROOT=root;
 const file=(n:string,v:any)=>{const p=join(root,n);writeFileSync(p,JSON.stringify(v));return p;};const out=join(root,'out');const lines:string[]=[];
 try{expect(await cli.main(['--base','http://127.0.0.1:1','--cookies',file('cookies.txt',''),'--out',out,'--prompts-file',file('prompts.json',[]),'--set','holdout','--holdout-sha','0'.repeat(64),'--budget-usd','1','--cost-ledger',file('ledger.json',ledger()),'--snapshot-manifest',file('snapshot.json',{}),'--edits-file',file('edits.json',[])],x=>lines.push(x))).toBe(2);expect(lines.at(-1)).toContain('sealed_hash');expect(existsSync(out)).toBe(false);}finally{if(previous===undefined)delete process.env.FANOUT_HOLDOUT_OUT_ROOT;else process.env.FANOUT_HOLDOUT_OUT_ROOT=previous;}
});
it('reports a stale lock category without printing paths or reasons',async()=>{
 const root=resolve('.superpowers/eval/locking-'+crypto.randomUUID());roots.push(root);mkdirSync(root,{recursive:true});const path=join(root,'ledger.json');const l=ledger();transport.beginPrompt(l,'unknown');writeFileSync(path,JSON.stringify(l));writeFileSync(path+'.lock','');
 const lines:string[]=[];expect(await cli.main(['--settle','unknown','--cost-usd','1','--reason','private reason','--cost-ledger',path],x=>lines.push(x))).toBe(2);
 expect(lines.at(-1)).toContain('ledger_lock');expect(lines.join('')).not.toContain(path);expect(lines.join('')).not.toContain('private reason');expect(existsSync(path+'.lock')).toBe(true);
});
it('blocks unknown HTTP usage until settlement and preserves the cap across CLI invocations',async()=>{
 for(const failure of ['unauthorized','rate_limited']){
  const root=resolve('.superpowers/eval/http-settlement-'+crypto.randomUUID());roots.push(root);mkdirSync(root,{recursive:true});
  const file=(n:string,v:any)=>{const p=join(root,n);writeFileSync(p,JSON.stringify(v));return p;};const path=file('ledger.json',ledger());const cookies=join(root,'cookies.txt');writeFileSync(cookies,'');
  const end=BigInt(Date.now())*1_000_000n;const snapshot=file('snapshot.json',{source_hash:'0'.repeat(64),start_ns:String(end-3_600_000_000_000n),end_ns:String(end),shift_ns:'0',replayed_at:new Date().toISOString()});
  const prompts=file('prompts.json',Array.from({length:10},(_,i)=>({id:String(i),prompt:'Fixture '+i})));const edits=file('edits.json',mockEdits());const mock=startMock(failure as any);
  const args=(name:string,cap:string)=>['--base',mock.base,'--cookies',cookies,'--out',join(root,name),'--prompts-file',prompts,'--budget-usd',cap,'--cost-ledger',path,'--snapshot-manifest',snapshot,'--edits-file',edits];
  try{
   expect(await cli.main(args('first','1'),()=>{})).toBe(2);expect(mock.stats.posts).toBe(1);
   expect(await cli.main(args('unsettled','1'),()=>{})).toBe(2);expect(mock.stats.posts).toBe(1);
   const stored=JSON.parse(readFileSync(path,'utf8'));expect(stored.prompts[0].prompt_id).toBe('0');
   expect(await cli.main(['--settle','0','--cost-usd','.25','--reason','Controller confirmed charges','--cost-ledger',path],()=>{})).toBe(0);
   expect(await cli.main(args('under-cap','.849'),()=>{})).toBe(2);expect(mock.stats.posts).toBe(1);
   const evidence=JSON.parse(readFileSync(join(root,'under-cap/default/summary.json'),'utf8'));expect(evidence.manual_settlements).toHaveLength(1);expect(evidence.actual_cost_usd).toBe(.25);
   expect(await cli.main(args('at-cap','.85'),()=>{})).toBe(2);expect(mock.stats.posts).toBe(2);
  }finally{mock.server.stop(true);}
 }
});

it('compares added stat defaults against the fields authored before the server save',()=>{
 const g=golden();expect(g.authored_spec.panels[0].width).toBeUndefined();expect(g.authored_spec.panels[0].reduce).toBeUndefined();
 const built=cli.buildEdit(g.added_spec,{operation:'add',panel:g.stat_add_panel});expect(g.stat_add_panel.reduce).toBeUndefined();
 expect(compareEdit(g.added_spec,g.stat_added_spec,built.expected,'add').passed).toBe(true);
 const bad=structuredClone(g.stat_added_spec);bad.panels.at(-1).reduce='last';expect(compareEdit(g.added_spec,bad,built.expected,'add').passed).toBe(false);
});

it('checks an explicit CLI model label even on zero-dollar mock calls',async()=>{
 expect(await cli.main(['--mock','--no-output','--model-label','mock:wrong-model'],()=>{})).toBe(2);
});
