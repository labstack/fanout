import { afterEach, expect, it } from 'bun:test';
import { existsSync, mkdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import {tmpdir} from 'node:os';
import {createHash} from 'node:crypto';
import { join, resolve } from 'node:path';
import { parseOptions, buildEdit, exitCode, publicEvidence, main, modelIdentityMatches } from './main';
import { mockEvaluation, verifyMock, startMock, mockEdits } from './mock';
const dirs:string[]=[];afterEach(()=>dirs.splice(0).forEach(p=>rmSync(p,{recursive:true,force:true})));
it('accepts only documented options and rejects unsafe labels, origins and budgets',()=>{
  expect(parseOptions(['--help'])).toEqual({help:true});expect(parseOptions(['--mock'])).toEqual({mock:true});
  for(const args of [['--prompts','all'],['--base'],['--mock','--mock'],['--model-label','../unsafe'],['--budget-usd','NaN'],['--budget-usd','-1'],['--base','http://user:secret@localhost']])expect(()=>parseOptions(args)).toThrow();
});
it('builds private exact edit expectations from actual saved IDs before submission',()=>{
  const spec={name:'Mock',panels:[{id:'actual-id',title:'Before',thresholds:[{value:50}],unit:'ms'}]};
  const e=buildEdit(spec,{operation:'title',panel_index:0,value:'After'});
  expect(e.expected.panels[0].id).toBe('actual-id');expect(e.expected.panels[0].title).toBe('After');expect(e.prompt).toContain('actual-id');
  expect(spec.panels[0].title).toBe('Before');
  expect(()=>buildEdit(spec,{operation:'title',panel_index:0,value:'Before'})).toThrow();
});
it('runs ten creates and five consecutive edits through loopback HTTP and the scorer',async()=>{
  const result=await mockEvaluation();expect(result.exit_code).toBe(0);expect(result.evidence.score.passed).toBe(true);
  expect(result.evidence.runs).toHaveLength(10);expect(result.evidence.edits).toHaveLength(5);
  expect(result.evidence.actual_cost_usd).toBe(0);expect(result.evidence.observed_configuration).toEqual([{provider:'mock',model:'mock-no-provider'}]);
  expect(result.evidence.edits.map((e:any)=>e.saved.version)).toEqual([2,3,4,5,6]);
});
it('gives every injected scoring and transport failure a nonzero exit',async()=>{expect(await verifyMock()).toBeGreaterThanOrEqual(10)});
it.each(['model_mismatch','mixed_models','configuration_changed'] as const)('stops spending immediately on %s',async failure=>{
  const result=await mockEvaluation(failure,{model_label:undefined});
  expect(result.exit_code).toBe(2);
  expect(result.stats.posts).toBe(failure==='model_mismatch'?1:10);
  expect(result.stats.edits).toBe(0);
  expect(result.evidence.model_mismatch.length).toBeGreaterThan(0);
});
it('leaves a disconnect after usage unsettled without posting another prompt',async()=>{
  const result=await mockEvaluation('missing_terminal');
  expect(result.stats.posts).toBe(1);
  expect(result.evidence.runs[0].calls).toHaveLength(1);
  expect(result.evidence.runs[0].calls[0].usage).not.toBeNull();
  expect(result.evidence.runs[0].settlement_status).toBe('unknown');
  expect(result.evidence.cost_complete).toBe(false);
  expect(result.evidence.actual_cost_usd).toBeNull();
  expect(result.evidence.metered_cost_usd).toBe(0);
});
it('stops subsequent edits on a mismatching call and checks an explicit configured model',async()=>{
  const edit=await mockEvaluation('edit_model_mismatch');
  expect(edit.stats.posts).toBe(12);expect(edit.stats.edits).toBe(2);expect(edit.exit_code).toBe(2);
  const config=await mockEvaluation('configuration_changed');
  expect(config.stats.posts).toBe(10);expect(config.stats.edits).toBe(0);expect(config.exit_code).toBe(2);
});
it.each(['dated_model','iso_dated_model'] as const)('accepts only a dated snapshot of the model label and records both identities (%s)',async failure=>{
  const result=await mockEvaluation(failure);expect(result.exit_code).toBe(0);expect(result.stats.posts).toBe(15);
  expect(result.evidence.model_label).toBe('mock:mock-no-provider');
  expect(result.evidence.runs[0].expected_model).toBe('mock:mock-no-provider');
  expect(result.evidence.runs[0].calls[0].model).toBe('mock-no-provider-'+(failure==='dated_model'?'20261008':'2026-10-08'));
  expect(result.evidence.observed_configuration[0].model).toBe(result.evidence.runs[0].calls[0].model);
});
it.each(['wrong_suffix','invalid_date','model_mismatch'] as const)('rejects %s without further spend',async failure=>{
  const result=await mockEvaluation(failure);expect(result.exit_code).toBe(2);expect(result.stats.posts).toBe(1);expect(result.evidence.model_mismatch.length).toBeGreaterThan(0);
});
it('matches exact provider identities and valid dated snapshots of the same label',()=>{
  expect(modelIdentityMatches('anthropic:claude-sonnet-5-5','anthropic:claude-sonnet-5-5-20261008')).toBe(true);
  expect(modelIdentityMatches('openai:gpt-6.1-sol','openai:gpt-6.1-sol-2026-10-08')).toBe(true);
  expect(modelIdentityMatches('openai:gpt-6.1-sol-2026-10-08','openai:gpt-6.1-sol-2026-10-08')).toBe(true);
  for(const observed of ['anthropic:gpt-6.1-sol-2026-10-08','openai:gpt-6.1-sol-mini-2026-10-08','openai:gpt-6.1-sol-2026-02-30','openai:gpt-6.1-sol-20261301','openai:gpt-6.1-sol-latest'])expect(modelIdentityMatches('openai:gpt-6.1-sol',observed)).toBe(false);
  expect(modelIdentityMatches(undefined,'openai:gpt-6.1-sol')).toBe(false);
  expect(modelIdentityMatches('openai:gpt-6.1-sol',undefined)).toBe(false);
});
it('exports only aggregate results and prompt IDs/hashes for the sealed set',()=>{
  const raw={schema:3,set:'holdout',runs:[{prompt_id:'id',prompt_sha:'hash',prompt:'private prompt',saved:{spec:'private'},error:'private failure'}],edits:[{prompt:'private edit'}],score:{passed:false},model_label:'label',observed_configuration:[],actual_cost_usd:0};
  const result=publicEvidence(raw);expect(result.runs).toEqual([{prompt_id:'id',prompt_sha:'hash'}]);expect(result.edits).toBeUndefined();expect(JSON.stringify(result)).not.toContain('private');
});
it('imports safely, prints help without HTTP and refuses missing private inputs',async()=>{
  expect(await main(['--help'],()=>{})).toBe(0);expect(await main(['--budget-usd','0'],()=>{})).toBe(2);
  expect(exitCode({passed:true},true)).toBe(2);expect(exitCode({passed:false},false)).toBe(1);
});
it('gates real CLI HTTP at zero balance and reuses persisted prompt costs across invocations',async()=>{
  const root=resolve('.superpowers/eval/cli-test-'+crypto.randomUUID());dirs.push(root);mkdirSync(root,{recursive:true});
  const file=(name:string,data:any)=>{const p=join(root,name);writeFileSync(p,JSON.stringify(data));return p;};
  const ledger=file('ledger.json',{schema:1,rates:[{provider:'mock',model:'mock-no-provider',verified_at:'2026-10-08T00:00:00Z',input_includes_cache:true,input:0,output:0,cache_read:0,cache_write:0}],calls:[],prompts:[]});
  const prompts=file('prompts.json',Array.from({length:10},(_,i)=>({id:String(i),prompt:'Fixture '+i,expect:'dashboard',rationale:'Synthetic benchmark'})));
  const edits=file('edits.json',mockEdits());const snapshot=file('snapshot.json',{source_hash:'0'.repeat(64),start_ns:String(BigInt(Date.now()-3_600_000)*1_000_000n),end_ns:String(BigInt(Date.now())*1_000_000n),shift_ns:'0',replayed_at:new Date().toISOString()});
  const mock=startMock();try {
    const args=(label:string,budget:string)=>['--base',mock.base,'--cookies',file('cookies.txt',''),'--model-label','mock:mock-no-provider','--out',join(root,label),'--prompts-file',prompts,'--budget-usd',budget,'--cost-ledger',ledger,'--snapshot-manifest',snapshot,'--edits-file',edits];
    // Empty cookie jar is sufficient for this unauthenticated loopback mock.
    const blocked=args('blocked','0');writeFileSync(join(root,'cookies.txt'),'');
    expect(await main(blocked,()=>{})).toBe(2);expect(mock.stats.posts).toBe(0);
    const invalidEdits=mockEdits();invalidEdits[3].panel_id='actual_latency';writeFileSync(edits,JSON.stringify(invalidEdits));
    const invalid=args('invalid-edits','0.60');writeFileSync(join(root,'cookies.txt'),'');expect(await main(invalid,()=>{})).toBe(2);expect(mock.stats.posts).toBe(0);
    writeFileSync(edits,JSON.stringify(mockEdits()));
    const passing=args('passing','0.60');writeFileSync(join(root,'cookies.txt'),'');expect(await main(passing,()=>{})).toBe(0);expect(mock.stats.posts).toBe(15);
    const saved=JSON.parse(readFileSync(ledger,'utf8'));expect(saved.prompts).toHaveLength(15);expect(saved.calls).toHaveLength(15);expect(saved.prompts.every((p:any)=>p.settled)).toBe(true);
    const again=args('again','0');writeFileSync(join(root,'cookies.txt'),'');expect(await main(again,()=>{})).toBe(0);expect(mock.stats.posts).toBe(30);
    const evidence=JSON.parse(readFileSync(join(root,'again/mock:mock-no-provider/summary.json'),'utf8'));expect(evidence.schema).toBe(3);expect(evidence.candidate_source_hash).not.toBe('0'.repeat(64));expect(JSON.stringify(evidence)).not.toContain('"values"');
    expect(await main(again,()=>{})).toBe(2);expect(mock.stats.posts).toBe(30);
  }finally{mock.server.stop(true);}
});

it('requires explicit intents while keeping benchmark and edit counts strict',async()=>{
 const {validateInputs}=await import('./main');
 const end=BigInt(Date.now())*1_000_000n;
 const snapshot={source_hash:'0'.repeat(64),start_ns:String(end-1000000000n),end_ns:String(end),shift_ns:'0',replayed_at:new Date().toISOString()};
 const prompt={id:'synthetic',prompt:'Explain a percentile.',expect:'answer',rationale:'Answer only'};
 expect(()=>validateInputs([prompt],[],snapshot,'holdout')).not.toThrow();
 for(const patch of [{expect:undefined},{rationale:undefined},{expect:'other'},{id:''}])expect(()=>validateInputs([{...prompt,...patch}],[],snapshot,'holdout')).toThrow();
 expect(()=>validateInputs([prompt,prompt],[],snapshot,'holdout')).toThrow();
 expect(()=>validateInputs([prompt],mockEdits(),snapshot,'benchmark')).toThrow();
 const ten=Array.from({length:10},(_,i)=>({...prompt,id:String(i),expect:'dashboard'}));
 expect(()=>validateInputs(ten,mockEdits(),snapshot,'benchmark')).not.toThrow();
 expect(()=>validateInputs(ten,[],snapshot,'benchmark')).toThrow();
 expect(()=>validateInputs([...ten.slice(0,9),prompt],mockEdits(),snapshot,'benchmark')).toThrow();
 expect(()=>parseOptions(['--set','parity'])).toThrow();
});

it('runs optional mock judging through main on the shared ledger and retains explicit skips',async()=>{
 const root=resolve('.superpowers/eval/judge-cli-'+crypto.randomUUID());dirs.push(root);
 expect(await main(['--mock','--judge','--out',root],()=>{})).toBe(0);
 const result=JSON.parse(readFileSync(join(root,'mock/summary.json'),'utf8'));
 expect(result.judge_summary).toMatchObject({status:'complete',paired:10,cost_usd:0});
 expect(result.actual_cost_usd).toBe(0);expect(result.judgments.records).toHaveLength(10);
 const skipped=await mockEvaluation();expect(skipped.evidence.judge_summary).toMatchObject({status:'skipped',reason:'not_requested',cost_usd:0,combined_overall:null});
});
it('whitelists intent and judge aggregates without leaking rationale, errors or evidence',()=>{
 const sentinel='PRIVATE_SENTINEL';
 const raw={set:'holdout',runs:[{prompt_id:'id',prompt_sha:'hash',expect:sentinel,rationale:sentinel,final_text:sentinel,checks:[{error:sentinel}]}],score:{intent_accuracy:1},judgments:{records:[{rationale:sentinel,scores:sentinel}]},judge_summary:{status:'complete',paired:1,cost_usd:0,combined_overall:4}};
 expect(JSON.stringify(publicEvidence(raw))).not.toContain(sentinel);
 expect(publicEvidence(raw).judge_summary).toMatchObject({paired:1,cost_usd:0});
});

it('scores a mixed synthetic holdout with eleven dashboards and five answers without edits',async()=>{
 const prompts=Array.from({length:16},(_,i)=>({id:'synthetic-'+i,prompt:'Synthetic intent '+i,expect:i<11?'dashboard' as const:'answer' as const,rationale:'Unsealed fixture'}));
 const result=await mockEvaluation(undefined,{prompts,set:'holdout',edits:[],judge:true});
 expect(result.exit_code).toBe(0);expect(result.stats.posts).toBe(16);expect(result.stats.creates).toBe(11);expect(result.stats.edits).toBe(0);
 expect(result.evidence.score).toMatchObject({dashboard_prompts:11,answer_prompts:5,answer_rate:1,dashboard_rate:1,intent_accuracy:1,s5:null,passed:true});
 expect(result.evidence.runs.every((r:any)=>r.calls.length===1&&r.settlement_status==='completed')).toBe(true);
 expect(result.evidence.judge_summary).toMatchObject({status:'complete',paired:11,cost_usd:0});
});
it.each(['answer_create','answer_replace','answer_restore','two_boards','thread_failure','blank_answer','missing_terminal','usage_failure','unauthorized','duplicate_check'])('rejects synthetic intent failure %s while retaining metering',async failure=>{
 const prompts=[{id:'synthetic',prompt:'Synthetic answer',expect:failure==='duplicate_check'?'dashboard' as const:'answer' as const,rationale:'Unsealed fixture'}];
 const result=await mockEvaluation(failure as any,{prompts,set:'holdout',edits:[]});
 expect(result.exit_code).not.toBe(0);expect(result.evidence.score.passed).toBe(false);
 if(failure!=='unauthorized')expect(result.evidence.runs[0].calls).toHaveLength(1);
 if(['answer_create','answer_replace','answer_restore','two_boards'].includes(failure))expect(result.evidence.runs[0].mutation_observed).toBe(true);
 if(failure==='two_boards')expect(result.evidence.runs[0].saved).toBe(false);
});

it('runs synthetic holdout CLI without reading edits and skips a missing OpenAI key with null quality and zero judge spend',async()=>{
 const root=join(realpathSync(tmpdir()),'synthetic-intents-'+crypto.randomUUID());dirs.push(root);mkdirSync(root);
 const sentinel='UNSEALED_PRIVATE_SENTINEL';
 const prompts=Array.from({length:16},(_,i)=>({id:'synthetic-'+i,prompt:sentinel+' '+i,expect:i<11?'dashboard' as const:'answer' as const,rationale:sentinel}));
 const mock=startMock(undefined,prompts);
 const file=(n:string,v:any)=>{const path=join(root,n);writeFileSync(path,JSON.stringify(v));return path;};
 const promptFile=file('synthetic.json',prompts),raw=readFileSync(promptFile),end=BigInt(Date.now())*1_000_000n;
 const ledgerPath=file('ledger.json',{schema:1,rates:[{provider:'mock',model:'mock-no-provider',verified_at:'2026-10-08T00:00:00Z',input_includes_cache:true,input:0,output:0,cache_read:0,cache_write:0}],calls:[],prompts:[]});
 const cookies=join(root,'cookies.txt');writeFileSync(cookies,'');
 const envKeys=['FANOUT_HOLDOUT_OUT_ROOT','JUDGE_ANTHROPIC_KEY','JUDGE_OPENAI_KEY'];const previous=envKeys.map(k=>process.env[k]);
 process.env.FANOUT_HOLDOUT_OUT_ROOT=root;process.env.JUDGE_ANTHROPIC_KEY='mock-only';delete process.env.JUDGE_OPENAI_KEY;
 try {
  expect(await main(['--base',mock.base,'--cookies',cookies,'--out',join(root,'out'),'--prompts-file',promptFile,'--set','holdout','--holdout-sha',createHash('sha256').update(raw).digest('hex'),'--budget-usd','.6','--cost-ledger',ledgerPath,'--snapshot-manifest',file('snapshot.json',{source_hash:'0'.repeat(64),start_ns:String(end-1000000000n),end_ns:String(end),shift_ns:'0',replayed_at:new Date().toISOString()}),'--edits-file',join(root,'deliberately-absent.json'),'--judge'],()=>{})).toBe(0);
  const result=JSON.parse(readFileSync(join(root,'out/default/summary.json'),'utf8'));
  expect(result.score).toMatchObject({dashboard_prompts:11,answer_prompts:5,intent_accuracy:1,s5:null});
  expect(result.judge_summary).toMatchObject({status:'skipped',reason:'judge_keys_unconfigured',combined_overall:null,cost_usd:0,paired:0});
  expect(result.actual_cost_usd).toBe(0);expect(JSON.stringify(result)).not.toContain(sentinel);expect(JSON.stringify(result)).not.toContain('mock-only');
  expect(existsSync(join(root,'out/default/judge-evidence.json'))).toBe(false);
  expect(JSON.parse(readFileSync(ledgerPath,'utf8')).calls).toHaveLength(16);expect(mock.stats.edits).toBe(0);
 }finally{mock.server.stop(true);envKeys.forEach((k,i)=>{if(previous[i]===undefined)delete process.env[k];else process.env[k]=previous[i]});}
});
