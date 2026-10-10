import {afterEach,expect,it} from 'bun:test';
import {runJudges,prepareJudgeEvidence,MODELS,CRITERIA,RUBRIC,SCHEMA,type JudgeConfig} from './judge';
import {startJudgeMock} from './judge-mock';
import {beginPrompt,recordUsage,settlePrompt,spentUSD,validateLedger,type Ledger} from './transport';
const mocks:ReturnType<typeof startJudgeMock>[]=[];
afterEach(()=>mocks.splice(0).forEach(m=>m.server.stop(true)));
const evidence=[{user_question:'Synthetic unsealed fixture',expect:'dashboard',assistant_text:'Saved.',dashboard_spec:{panels:[]},panel_results:[]}];
function setup(failure?:string,zero=false){
 const mock=startJudgeMock(failure);mocks.push(mock);
 const ledger:Ledger={schema:1,rates:Object.entries(MODELS).map(([provider,model])=>({provider,model,verified_at:'2026-10-08T00:00:00Z',input_includes_cache:provider==='openai',input:zero?0:1,output:zero?0:2,cache_read:zero?0:.1,cache_write:zero?0:1.5})),calls:[],prompts:[]};
 let persisted=0;
 const config:JudgeConfig={env:{JUDGE_ANTHROPIC_KEY:'mock-only-anthropic',JUDGE_OPENAI_KEY:'mock-only-openai'},endpoints:{anthropic:mock.base,openai:mock.base},ledger,cap_usd:5,persist_ledger:()=>{persisted++},fetch,evidence,mock:true};
 return {mock,config,ledger,persisted:()=>persisted};
}
it('skips both judges before transport or ledger changes when either key is missing',async()=>{
 const {config,ledger,persisted}=setup();
 for(const env of [{},{JUDGE_ANTHROPIC_KEY:'mock-only'},{JUDGE_OPENAI_KEY:'mock-only'}]){
  let requests=0;
  expect(await runJudges({...config,env,fetch:async()=>{requests++;throw new Error('Transport must not run')}})).toEqual({status:'skipped',reason:'judge_keys_unconfigured',records:[],cost_usd:0});
  expect(requests).toBe(0);expect(persisted()).toBe(0);expect(ledger.calls).toEqual([]);expect(ledger.prompts).toEqual([]);
 }
});
it('keeps judge evidence minimal and uses the immutable saved record',()=>{
 const sentinel='PRIVATE_SENTINEL';
 expect(prepareJudgeEvidence([{expect:'dashboard',prompt:sentinel,final_text:'Saved',checked:true,saved_record:{spec:{panels:[]}},checks:[{id:'p',status:'error',rows:0,error:'Safe',sql:sentinel,frame:sentinel}],rationale:sentinel,tools:sentinel},{expect:'answer',prompt:sentinel}])).toEqual([{user_question:sentinel,expect:'dashboard',assistant_text:'Saved',dashboard_spec:{panels:[]},panel_results:[{id:'p',status:'error',rows:0,error:'Safe'}]}]);
});
it('sends unchanged rubric schemas and byte-identical inputs to distinct evaluation models',async()=>{
 const {mock,config,ledger}=setup();const result=await runJudges(config);
 expect(result.status).toBe('complete');expect(mock.stats.assertions).toEqual([]);expect(mock.stats.inputs.anthropic).toEqual(mock.stats.inputs.openai);
 expect(mock.stats.bodies[0].system).toBe(RUBRIC);expect(mock.stats.bodies[1].response_format.json_schema.schema).toEqual(SCHEMA);
 expect(result.records[0]).toMatchObject({combined_overall:3.5,disagreement:true,criterion_mean:{answers_question:3}});
 expect(ledger.calls).toHaveLength(2);expect(ledger.calls[0].usage).toMatchObject({input_tokens:10,output_tokens:5,cache_read_tokens:2,cache_write_tokens:3});
 expect(ledger.calls[1].usage).toMatchObject({input_tokens:20,output_tokens:10,cache_read_tokens:4,reasoning_tokens:6});
 expect(result.cost_usd).toBeCloseTo(.0000611,9);expect(spentUSD(ledger)).toBeCloseTo(result.cost_usd!,9);expect(()=>validateLedger(ledger)).not.toThrow();
 expect(JSON.stringify(result)).not.toContain('mock-only');expect(JSON.stringify(result)).not.toContain(mock.base);
});
it.each(['malformed','truncated','refusal','invalid_score','blank_rationale','extra_key'])('charges %s usage before rejecting quality and retains the other judge',async failure=>{
 const {config,ledger,mock}=setup(failure);const result=await runJudges(config);
 expect(result.status).toBe('incomplete');expect(result.records[0]).toMatchObject({combined_overall:null,disagreement:null});
 expect(Object.values(result.records[0].criterion_mean).every(v=>v===null)).toBe(true);
 expect(result.records[0].judges.anthropic.scores).not.toBeNull();expect(result.records[0].judges.openai.scores).toBeNull();
 expect(ledger.calls).toHaveLength(2);expect(result.cost_usd).toBeGreaterThan(0);expect(mock.stats.requests).toBe(2);expect(ledger.prompts.every(p=>p.settled)).toBe(true);
});
it.each(['http_unknown','transport','missing_usage','bad_usage','invalid_json'])('stops all further calls after %s with an unsettled reservation',async failure=>{
 const {config,ledger,mock}=setup(failure);
 const result=await runJudges({...config,evidence:[...evidence,...evidence],...(failure==='transport'?{fetch:async(url:string,init:RequestInit)=>{await fetch(url,init);throw new Error('mock-only-anthropic')}}:{})});
 expect(result.status).toBe('incomplete');expect(result.cost_usd).toBeNull();expect(ledger.prompts.at(-1)?.settled).toBe(false);expect(mock.stats.requests).toBe(1);
 expect(result.records[0].judges.openai.scores).toBeNull();expect(JSON.stringify(result)).not.toContain('mock-only');
});
it('meters known nonbillable retries and a single temperature fallback',async()=>{
 const {config,ledger,mock}=setup('retry');const result=await runJudges(config);
 expect(result.status).toBe('complete');expect(mock.stats.requests).toBe(4);expect(ledger.calls).toHaveLength(4);
 expect(ledger.calls.filter(c=>c.status==='error').every(c=>c.cost_usd===0)).toBe(true);
 expect(new Set(ledger.calls.map(c=>c.run_id+':'+c.step)).size).toBe(4);expect(()=>validateLedger(ledger)).not.toThrow();
 expect(mock.stats.bodies.at(-1).temperature).toBeUndefined();
});
it('bounds retries and records one judge failure without combined quality',async()=>{
 const {config,ledger,mock}=setup('exhausted');const result=await runJudges(config);
 expect(result.status).toBe('incomplete');expect(mock.stats.requests).toBe(5);expect(ledger.calls).toHaveLength(5);expect(result.records[0].combined_overall).toBeNull();expect(ledger.prompts.every(p=>p.settled)).toBe(true);
});
it('uses the retained ledger cap and conservative reserve before requests',async()=>{
 const {config,ledger,mock}=setup();
 beginPrompt(ledger,'agent');recordUsage(ledger,{run_id:'agent',step:1,provider:'openai',model:MODELS.openai,status:'completed',usage:{input_tokens:1000000,output_tokens:0,cache_read_tokens:0,cache_write_tokens:0,reasoning_tokens:0}});settlePrompt(ledger,'agent',true);
 const result=await runJudges({...config,cap_usd:1});expect(result.status).toBe('incomplete');expect(mock.stats.requests).toBe(0);expect(spentUSD(ledger)).toBe(1);
});
it('refuses external mock endpoints and differing configured identities before transport',async()=>{
 const {config,mock}=setup();
 for(const patch of [{endpoints:{...config.endpoints,openai:'https://provider.invalid'}},{env:{...config.env,JUDGE_OPENAI_MODEL:'different-model'}}]) {
  expect((await runJudges({...config,...patch})).status).toBe('incomplete');expect(mock.stats.requests).toBe(0);
 }
});

it('freezes differing configured judge identities before any comparison',async()=>{
 const {config,mock}=setup();const result=await runJudges({...config,env:{...config.env,JUDGE_OPENAI_MODEL:'different-model'}});
 expect(result).toMatchObject({status:'incomplete',reason:'judge_model_mismatch',configured_models:{anthropic:MODELS.anthropic,openai:'different-model'}});
 expect(mock.stats.requests).toBe(0);expect(result.records).toEqual([]);
});

it('does not retry a temperature rejection with billable usage',async()=>{
 const {config,mock,ledger}=setup('paid_temperature');const result=await runJudges(config);
 expect(result.status).toBe('incomplete');expect(mock.stats.requests).toBe(2);
 expect(ledger.calls[1].cost_usd).toBeGreaterThan(0);expect(result.records[0].judges.openai.temperature_retried).toBe(false);
});
it('records a differing response model with unknown-rate cost and stops further calls',async()=>{
 const {config,mock,ledger}=setup('response_model');const result=await runJudges(config);
 expect(result.status).toBe('incomplete');expect(mock.stats.requests).toBe(1);expect(result.cost_usd).toBeNull();
 expect(ledger.calls[0].model).toBe('different-model');expect(ledger.calls[0].usage).not.toBeNull();
});
