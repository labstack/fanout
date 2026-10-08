import { closeSync, existsSync, openSync, readFileSync, renameSync, unlinkSync, writeFileSync } from 'node:fs';
import { join, resolve, relative } from 'node:path';
import { createHash } from 'node:crypto';
import { changedPanels, compareEdit, score, stable, type Operation, type Run, type Spec } from './score';
import { beginPrompt, canStartPrompt, cookieHeader, executePanels, findSaved, noSymlinks, originURL, privateFile, readSSE, recordUsage, requestJSON, requestResponse, safeOutput, validateOutput, manualSettlement, settlePrompt, spentUSD, validateLedger, type Ledger, type ObjectValue, type StreamState } from './transport';

const flags=new Set(['help','mock','no-output']);
const values=new Set(['base','cookies','model-label','out','prompts-file','set','budget-usd','cost-ledger','snapshot-manifest','edits-file','holdout-sha','settle','cost-usd','reason']);
export const HELP=`Usage: bun scripts/dashboard-eval/main.ts [options]
  --help                         Print this help; no HTTP
  --mock                         Offline loopback fixtures; no provider, $0
  --no-output                    Skip mock artifact writes (CI)
  --settle ID --cost-usd USD --reason TEXT --cost-ledger PATH
                                 Controller settlement of unknown prompt cost
  --base ORIGIN                  Controller's disposable Fanout origin
  --cookies PATH                 Private Netscape cookie jar
  --model-label LABEL            Expected model or provider:model (does not select it)
  --out PATH                     Fresh run parent; benchmark: .superpowers/eval/
  --prompts-file PATH            Private JSON prompt array
  --set benchmark|holdout        Default: benchmark; holdout is controller-only
  --budget-usd USD               Whole-ledger task cap; required for real runs
  --cost-ledger PATH             Private ledger with verified per-provider rates
  --snapshot-manifest PATH       Immutable source/replay hash and signed ns bounds
  --edits-file PATH              Five private exact edits: title,threshold,add,remove,unit
  --holdout-sha SHA256           Frozen prompt file hash; required for holdout
Exit: 0 complete pass; 1 measured failure; 2 invalid/incomplete/budget-blocked.
`;
export function parseOptions(args:string[]):ObjectValue {
  const opts:ObjectValue={};
  for(let i=0;i<args.length;i++) {
    if(!args[i].startsWith('--'))throw new Error('Invalid option');
    const key=args[i].slice(2);if(key in opts)throw new Error('Duplicate option');
    if(flags.has(key)){opts[key]=true;continue;}
    if(!values.has(key)||!args[i+1]||args[i+1].startsWith('--'))throw new Error('Unknown or incomplete option');
    opts[key]=args[++i];
  }
  if(opts.base)originURL(opts.base);
  if(opts['model-label']&&(!/^(?:[A-Za-z0-9][A-Za-z0-9._-]{0,79}:)?[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(opts['model-label'])||opts['model-label'].includes('..')))throw new Error('Unsafe model label');
  if(opts['budget-usd']!==undefined&&(!Number.isFinite(Number(opts['budget-usd']))||Number(opts['budget-usd'])<0))throw new Error('Invalid budget');
  if(opts.set&&!['benchmark','holdout'].includes(opts.set))throw new Error('Invalid set');
  if(opts['holdout-sha']&&!/^[a-f0-9]{64}$/.test(opts['holdout-sha']))throw new Error('Invalid sealed hash');
  return opts;
}
export type Prompt={id:string;prompt:string};
export type EditInput={operation:Operation;panel_id?:string;panel_index?:number;field?:string;value?:any;panel?:ObjectValue;after?:string};
export function buildEdit(before:Spec,input:EditInput):{expected:Spec;prompt:string} {
  const expected=structuredClone(before);
  const index=input.panel_id?expected.panels.findIndex(p=>p.id===input.panel_id):input.panel_index??-1;
  const panel=expected.panels[index];
  if(input.operation!=='add'&&!panel)throw new Error('Edit target not in saved dashboard');
  switch(input.operation) {
    case 'add':if(!input.panel?.id||expected.panels.some(p=>p.id===input.panel!.id))throw new Error('Invalid added panel');{const at=input.after?expected.panels.findIndex(p=>p.id===input.after)+1:expected.panels.length;if(input.after&&at===0)throw new Error('Invalid add placement');expected.panels.splice(at,0,structuredClone(input.panel) as Spec['panels'][number]);}break;
    case 'remove':expected.panels.splice(index,1);break;
    case 'title':if(typeof input.value!=='string'||!input.value.trim())throw new Error('Invalid title');panel.title=input.value;break;
    case 'threshold':if((input.field??'thresholds')!=='thresholds'||!Array.isArray(input.value)||!input.value.length)throw new Error('Invalid thresholds');panel.thresholds=structuredClone(input.value);break;
    case 'unit':if((input.field??'unit')!=='unit'||input.value===undefined)throw new Error('Invalid unit');panel[input.field??'unit']=structuredClone(input.value);break;
    case 'move':if(!input.value||typeof input.value!=='object')throw new Error('Invalid grid');panel.grid=structuredClone(input.value);break;
    default:throw new Error('Invalid edit operation');
  }
  if(stable(before)===stable(expected))throw new Error('No-op edit');
  const target=input.operation==='add'?input.panel!.id:panel.id;
  const change=input.operation==='add'?{add_panel:input.panel,...(input.after?{after:input.after}:{})}:input.operation==='remove'?{remove_panel_id:target}: {panel_id:target,field:input.operation==='title'?'title':input.operation==='threshold'?'thresholds':input.operation==='move'?'grid':input.field??'unit',value:input.value};
  // Authored before submission. No service recipes or benchmark prompt text in the CLI.
  return {expected,prompt:`Apply exactly this change to the saved dashboard: ${JSON.stringify(change)}. Preserve every unrelated authored field, dashboard metadata, time, annotations, variables and panel order. ${['add','remove','move'].includes(input.operation)?'Only physical grid packing may change for placement.':'Preserve all grids too.'}`};
}
export type Snapshot={source_hash:string;start_ns:string;end_ns:string;shift_ns:string;replayed_at:string};
const snapshotEvidence=(s:Snapshot|undefined)=>s?{source_hash:s.source_hash,start_ns:s.start_ns,end_ns:s.end_ns,shift_ns:s.shift_ns,replayed_at:s.replayed_at}:undefined;
export type EvaluationConfig={base:string;cookies:string;model_label?:string;prompts:Prompt[];edits:EditInput[];ledger:Ledger;cap_usd:number;snapshot:Snapshot;snapshot_manifest_hash:string;candidate_source_hash:string;set:string;mock?:boolean;persist_ledger?:()=>void};
export function exitCode(result:{passed:boolean},incomplete:boolean):number{return incomplete?2:result.passed?0:1}
const blankRun=():Run=>({complete:false,saved:false,elapsed_ms:null,valid:false,checked:false,panels:[],checks:[]});
const sha=(v:string|Uint8Array)=>createHash('sha256').update(v).digest('hex');
export async function runEvaluation(config:EvaluationConfig):Promise<{exit_code:number;evidence:ObjectValue}> {
  const started_at=new Date().toISOString(),runs:ObjectValue[]=[],edits:ObjectValue[]=[];
  let incomplete=false;
  let default_model:string|undefined;
  const model_mismatch:ObjectValue[]=[];
  const json=(path:string,init:RequestInit={})=>requestJSON(config.base,path,headers(init,path));
  function headers(init:RequestInit,path:string):RequestInit {return {...init,headers:{'Content-Type':'application/json','Fanout-Request':'1',Cookie:cookieHeader(config.cookies,new URL(path,config.base)),...Object.fromEntries(new Headers(init.headers))}};}
  const turn=async(prompt:Prompt,thread_id=crypto.randomUUID()):Promise<ObjectValue>=>{
    const run_id=crypto.randomUUID(),observed_at=new Date().toISOString(),post_started=performance.now();
    const result:ObjectValue={...blankRun(),prompt_id:prompt.id,prompt_sha:sha(prompt.prompt),thread_id,run_id,observed_at,saved_record:null,persisted_status:null,terminal_events:[],incomplete:false};
    if(!config.mock&&!canStartPrompt(config.ledger,config.cap_usd)){result.incomplete=true;result.error_code='budget_or_unsettled_usage';return result;}
    beginPrompt(config.ledger,run_id,prompt.id);config.persist_ledger?.();
    const signal=AbortSignal.timeout(240_000);
    let state:StreamState|undefined;
    try {
      const response=await requestResponse(config.base,'/api/agent/runs',headers({method:'POST',headers:{Accept:'text/event-stream'},body:JSON.stringify({threadId:thread_id,runId:run_id,messages:[{id:crypto.randomUUID(),role:'user',content:prompt.prompt}],state:{},tools:[],context:[],forwardedProps:{}}),signal},'/api/agent/runs'));
      const response_elapsed_ms=performance.now()-post_started;
      state=await readSSE(response,signal,u=>{if(u.run_id!==run_id)throw new Error('Wrong usage run');recordUsage(config.ledger,u);config.persist_ledger?.();});
      result.terminal_events=state.terminal?[state.terminal]:[];
      result.started_at=observed_at;result.stream_started_at=state.started_at;result.finished_at=state.finished_at;result.truncated=state.truncated;
      result.run_end_ms=performance.now()-post_started;
      const configured=state.configuration?`${state.configuration.provider}:${state.configuration.model}`:undefined;
      default_model??=configured;
      const expected=config.model_label?.includes(':')?config.model_label:config.model_label&&state.configuration?`${state.configuration.provider}:${config.model_label}`:default_model;
      result.configured_model=configured;result.expected_model=expected;
      for(const call of state.usage){const observed=`${call.provider}:${call.model}`;if(!expected||observed!==expected||call.provider==='unknown'||call.model==='unknown')model_mismatch.push({run_id,step:call.step,expected:expected??null,observed});}
      if(!configured||configured!==expected)model_mismatch.push({run_id,expected:expected??null,observed:configured??null});
      let messages:ObjectValue[]=[];
      try {const thread=await json('/api/agent/threads/'+encodeURIComponent(thread_id),{signal:AbortSignal.timeout(5000)});messages=thread.messages??[];}catch {result.thread_read_failed=true;}
      const save=findSaved(state,messages);
      if(save) {
        result.saved=true;result.saved_record=save.record;result.elapsed_ms=response_elapsed_ms+save.elapsed_ms;result.first_save_ms=response_elapsed_ms+save.first_elapsed_ms;result.panels=save.record.spec.panels;
        // Persisted version presence corroborates the immutable tool result. Never fetch the latest spec.
        const versions=await json('/api/dashboards/'+encodeURIComponent(save.record.id)+'/versions',{signal:AbortSignal.timeout(30_000)});
        const matches=versions.versions?.filter((v:any)=>v.version===save.record.version)??[];
        if(matches.length!==1)throw new Error('missing_persisted_version');
        result.version_evidence=matches[0];
        Object.assign(result,await executePanels(json,save.record.spec,AbortSignal.timeout(30_000)));
      }
      result.incomplete=model_mismatch.length>0||state.incomplete||(result.saved&&!result.checked);result.error_code=model_mismatch.length?'model_mismatch':state.error_code??(result.incomplete?'checks_incomplete':null);
    }catch {result.incomplete=true;result.error_code='request_or_evidence_incomplete';}
    const settled=settlePrompt(config.ledger,run_id,state?.terminal==='RUN_FINISHED'&&!state.incomplete,Boolean(state?.terminal||state?.truncated));
    const promptSettlement=config.ledger.prompts.find(p=>p.run_id===run_id)!;
    result.complete=promptSettlement.completed && state?.terminal==='RUN_FINISHED' && !state.incomplete;
    result.settlement_status=promptSettlement.status??'unknown';
    // Authoritative failed/incomplete usage is a measured failure, not unknown spend.
    if(settled && !promptSettlement.completed && (state?.truncated||state?.terminal==='RUN_ERROR'))result.incomplete=model_mismatch.length>0||Boolean(result.saved&&!result.checked);
    config.persist_ledger?.();result.cost_usd=config.ledger.prompts.find(p=>p.run_id===run_id)?.cost_usd??null;
    if(!settled){result.incomplete=true;result.error_code??='usage_incomplete';}
    if(result.incomplete) {
      // This read is a probe, never a retry of the mutation. Current servers may
      // lack the status read; unavailable evidence requires controller reconciliation.
      try {const status=await json('/api/agent/runs/'+encodeURIComponent(run_id),{signal:AbortSignal.timeout(5000)});result.persisted_status={checked:true,status:status.status};}
      catch {result.persisted_status={checked:false,status:'unavailable'};}
    }
    result.calls=config.ledger.calls.filter(c=>c.run_id===run_id);
    return result;
  };
  for(const prompt of config.prompts) {
    const r=await turn(prompt);runs.push(r);
    if(r.incomplete){incomplete=true;break;}
  }
  // One recorded dashboard, one thread, and five immediate before/after versions.
  const target=runs.find(r=>r.complete&&r.saved_record);
  if(!incomplete&&target) {
    let before=target.saved_record;
    for(const [index,input] of config.edits.entries()) {
      let built:{expected:Spec;prompt:string};
      try {built=buildEdit(before.spec,input);}catch {incomplete=true;break;}
      const r=await turn({id:`edit-${index+1}`,prompt:built.prompt},target.thread_id);
      const after=r.saved_record;
      const diff=after?compareEdit(before.spec,after.spec,built.expected,input.operation):{passed:false,changed_ids:[],expected_ids:changedPanels(before.spec.panels,built.expected.panels,['add','remove','move'].includes(input.operation)),layout_changed:false,expected_diff:{before:before.spec,after:built.expected},actual_diff:null};
      const metadata=(record:ObjectValue)=>Object.fromEntries(Object.entries(record).filter(([k])=>!['spec','version','updated_at'].includes(k)));
      const savedOK=after&&after.id===before.id&&after.version===before.version+1&&stable(metadata(before))===stable(metadata(after));
      edits.push({...diff,passed:Boolean(diff.passed&&savedOK&&r.checked&&r.valid&&r.complete&&!r.incomplete&&r.checks.length===r.panels.length),operation:input.operation,run_id:r.run_id,prompt_sha:r.prompt_sha,saved:after,version_evidence:r.version_evidence,checks:r.checks,terminal_events:r.terminal_events,calls:r.calls,cost_usd:r.cost_usd});
      if(r.incomplete||!after){incomplete||=r.incomplete;break;}
      before=after;
    }
  }
  const scored=score(runs as Run[],edits as any);
  const run_ids=new Set([...runs,...edits].map(r=>r.run_id));
  const observed_configuration=[...new Map(config.ledger.calls.filter(c=>run_ids.has(c.run_id)).map(c=>[`${c.provider}:${c.model}`,{provider:c.provider,model:c.model}])).values()];
  const cost_complete=config.ledger.prompts.every(p=>p.settled);
  if(model_mismatch.length){incomplete=true;Object.assign(scored,{s1:false,s2:false,s3:false,s4:false,s5:false,passed:false});}
  const manual_settlements=config.ledger.prompts.filter(p=>p.manual_settlement).map(p=>({run_id:p.run_id,prompt_id:p.prompt_id,...p.manual_settlement}));
  const evidence={schema:3,set:config.set,started_at,finished_at:new Date().toISOString(),model_label:config.model_label??null,default_model,model_mismatch,manual_settlements,observed_configuration,candidate_source_hash:config.candidate_source_hash,snapshot_manifest_hash:config.snapshot_manifest_hash,snapshot:snapshotEvidence(config.snapshot),runs,edits,score:scored,actual_cost_usd:cost_complete?spentUSD(config.ledger):null,metered_cost_usd:spentUSD(config.ledger),cost_complete,incomplete};
  return {exit_code:exitCode(scored,incomplete),evidence};
}
export function publicEvidence(e:ObjectValue):ObjectValue {
  if(e.set!=='holdout')return e;
  // A whitelist prevents individual failure details, specs and tool inputs leaking.
  return {schema:e.schema,set:e.set,started_at:e.started_at,finished_at:e.finished_at,model_label:e.model_label,default_model:e.default_model,model_mismatch_count:e.model_mismatch?.length??0,manual_settlement_count:e.manual_settlements?.length??0,observed_configuration:e.observed_configuration,candidate_source_hash:e.candidate_source_hash,snapshot_manifest_hash:e.snapshot_manifest_hash,snapshot:snapshotEvidence(e.snapshot),score:e.score,actual_cost_usd:e.actual_cost_usd,metered_cost_usd:e.metered_cost_usd,cost_complete:e.cost_complete,incomplete:e.incomplete,runs:e.runs.map((r:any)=>({prompt_id:r.prompt_id,prompt_sha:r.prompt_sha}))};
}
function inputJSON(path:string):any {return JSON.parse(readFileSync(privateFile(path),'utf8'))}
export function candidateSourceHash():string {
  const command=(args:string[])=>{const r=Bun.spawnSync(['git',...args],{stdout:'pipe',stderr:'pipe'});if(r.exitCode!==0)throw new Error('Cannot identify candidate source');return r.stdout;};
  const hash=createHash('sha256');hash.update(command(['rev-parse','HEAD']));
  // Enumerate only candidate source roots; never enumerate private eval inputs.
  const paths=command(['ls-files','-z','--cached','--others','--exclude-standard','--','AGENTS.md','go.mod','go.sum','justfile','scripts','internal','cmd','ui','docs','site']).toString().split('\0').filter(Boolean).sort();
  for(const path of paths) {if(path.toLowerCase().includes('holdout'))continue;hash.update(path+'\0');if(existsSync(path)){hash.update('file\0'+sha(readFileSync(privateFile(path)))+'\0');}else hash.update('deleted\0');}
  return hash.digest('hex');
}
// Five minutes covers replay/runner startup without accepting historical windows.
export function validateSnapshot(snapshot:Snapshot,started_at=Date.now()):void {
  if(!snapshot||!/^[a-f0-9]{64}$/.test(snapshot.source_hash)||!['start_ns','end_ns','shift_ns'].every(k=>typeof snapshot[k as keyof Snapshot]==='string'&&/^-?\d+$/.test(snapshot[k as keyof Snapshot]))||BigInt(snapshot.end_ns)<=BigInt(snapshot.start_ns)||!Number.isFinite(Date.parse(snapshot.replayed_at)))throw new Error('Invalid snapshot manifest');
  const now=BigInt(Math.trunc(started_at))*1_000_000n,tolerance=300_000_000_000n;
  if(now<BigInt(snapshot.start_ns)-tolerance||now>BigInt(snapshot.end_ns)+tolerance||Math.abs(started_at-Date.parse(snapshot.replayed_at))>300_000)throw new Error('Snapshot replay not near run start');
}
export function assertPromptHash(set:string,hash:string,expected?:string):void {
 if(set==='benchmark'&&hash.startsWith('a74ce656679ca792'))throw new Error('Sealed input refused');
 if(set==='holdout'&&(!expected||hash!==expected))throw new Error('Frozen sealed hash mismatch');
}
function validateInputs(prompts:any,edits:any,snapshot:any):void {
  if(!Array.isArray(prompts)||prompts.length!==10||prompts.some(p=>typeof p.id!=='string'||!p.id.trim()||typeof p.prompt!=='string'||!p.prompt.trim())||new Set(prompts.map(p=>p.id)).size!==10)throw new Error('Ten unique private prompts required');
  if(!Array.isArray(edits)||stable(edits.map(e=>e.operation))!==stable(['title','threshold','add','remove','unit']))throw new Error('Five consecutive edit operations required');
  if(typeof edits[2].panel?.id!=='string'||!edits[2].panel.id.trim()||edits[3].panel_id!==edits[2].panel.id)throw new Error('Remove must target the newly added panel');
  validateSnapshot(snapshot);
}
export async function main(args=process.argv.slice(2),log:(message:string)=>void=console.log):Promise<number> {
  let lock:string|undefined,lockFD:number|undefined,category='options';
  try {
    const opts=parseOptions(args);if(opts.help){log(HELP);return 0;}
    if(opts.mock) {
      if(Object.keys(opts).some(k=>!['mock','out','model-label','no-output'].includes(k)))throw new Error('Mock options');
      category='mock_verification';const {mockEvaluation,verifyMock}=await import('./mock');
      const checked=await verifyMock();const result=await mockEvaluation(undefined,opts['model-label']?{model_label:opts['model-label']}:{});result.evidence.candidate_source_hash=candidateSourceHash();
      if(!opts['no-output']){
        category='output';const out=safeOutput(join(opts.out??`.superpowers/eval/mock-${crypto.randomUUID()}`,opts['model-label']??'mock'),'benchmark');
        writeFileSync(join(out,'summary.json'),JSON.stringify(result.evidence,null,2)+'\n',{flag:'wx',mode:0o600});
      }
      log(result.exit_code===0?`MOCK PASS: ten saves, five edits, ${checked} failure injections, S1-S5 verified, $0`:`MOCK evaluation exit ${result.exit_code}; aggregate S1-S5 fail; $0`);return result.exit_code;
    }
    if(!opts.settle && (opts['cost-usd']!==undefined||opts.reason!==undefined))throw new Error('Settlement options require settle');
    if(opts['no-output'])throw new Error('Only mock can skip output');
    if(opts.settle){
      if(Object.keys(opts).some(k=>!['settle','cost-usd','reason','cost-ledger'].includes(k))||!opts['cost-ledger']||opts['cost-usd']===undefined||!opts.reason)throw new Error('Settlement inputs');
    }else for(const k of ['base','cookies','out','prompts-file','budget-usd','cost-ledger','snapshot-manifest','edits-file'])if(opts[k]===undefined)throw new Error('Missing private input');
    const set=opts.set??'benchmark';let out:string|undefined,prompts:any,edits:any,snapshot:Snapshot|undefined,snapshotRaw:Uint8Array|undefined,cookies='';
    if(!opts.settle){
      if(set==='holdout'){
        category='sealed_hash';if(!opts['holdout-sha'])throw new Error('Frozen hash required');
        category='output_root';if(!process.env.FANOUT_HOLDOUT_OUT_ROOT)throw new Error('Controller root required');
      }
      category='output';out=validateOutput(join(opts.out,opts['model-label']??'default'),set);
      category='sealed_hash';const promptPath=privateFile(opts['prompts-file']);
      // Check the name before opening. Content identity also rejects renamed sealed inputs.
      if(set==='benchmark'&&promptPath.toLowerCase().includes('holdout'))throw new Error('Sealed filename refused');
      const raw=readFileSync(promptPath);assertPromptHash(set,sha(raw),opts['holdout-sha']);
      category='private_inputs';prompts=JSON.parse(raw.toString());edits=inputJSON(opts['edits-file']);
      category='snapshot_manifest';snapshotRaw=readFileSync(privateFile(opts['snapshot-manifest']));snapshot=JSON.parse(Buffer.from(snapshotRaw).toString());validateInputs(prompts,edits,snapshot);
      cookies=readFileSync(privateFile(opts.cookies),'utf8');
    }
    category='ledger';const ledgerPath=privateFile(opts['cost-ledger']);
    if(set==='holdout'&&!relative(resolve(process.cwd()),ledgerPath).startsWith('..'))throw new Error('Private ledger location');
    category='ledger_lock';lock=noSymlinks(ledgerPath+'.lock');lockFD=openSync(lock,'wx',0o600);
    category='ledger';const ledger=inputJSON(ledgerPath) as Ledger;validateLedger(ledger);
    const persist=()=>{noSymlinks(ledgerPath);const temp=noSymlinks(ledgerPath+'.'+crypto.randomUUID()+'.tmp');writeFileSync(temp,JSON.stringify(ledger,null,2)+'\n',{flag:'wx',mode:0o600});renameSync(temp,ledgerPath);};
    if(opts.settle){category='settlement';manualSettlement(ledger,opts.settle,Number(opts['cost-usd']),opts.reason);persist();log(`Settlement recorded; total ledger spend $${spentUSD(ledger).toFixed(6)}`);return 0;}
    category='output';safeOutput(out!,set);
    category='evaluation';const result=await runEvaluation({base:opts.base,cookies,model_label:opts['model-label'],prompts,edits,ledger,cap_usd:Number(opts['budget-usd']),snapshot:snapshot!,snapshot_manifest_hash:sha(snapshotRaw!),candidate_source_hash:candidateSourceHash(),set,persist_ledger:persist});
    if(candidateSourceHash()!==result.evidence.candidate_source_hash){result.exit_code=2;result.evidence.incomplete=true;result.evidence.candidate_changed=true;}
    writeFileSync(join(out!,'summary.json'),JSON.stringify(publicEvidence(result.evidence),null,2)+'\n',{flag:'wx',mode:0o600});
    log(`Evaluation exit ${result.exit_code}; aggregate S1-S5 ${result.evidence.score.passed?'pass':'fail'}; cost ${result.evidence.actual_cost_usd===null?'incomplete':('$'+result.evidence.actual_cost_usd.toFixed(6))}`);
    return result.exit_code;
  }catch {log(`Evaluation exit 2; category: ${category}.`);return 2;}
  finally {if(lockFD!==undefined){closeSync(lockFD);if(lock)unlinkSync(lock);}}
}
if(import.meta.main)process.exitCode=await main();
