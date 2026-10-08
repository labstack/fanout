import { afterEach, expect, it } from 'bun:test';
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { parseOptions, buildEdit, exitCode, publicEvidence, main } from './main';
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
  const prompts=file('prompts.json',Array.from({length:10},(_,i)=>({id:String(i),prompt:'Fixture '+i})));
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
