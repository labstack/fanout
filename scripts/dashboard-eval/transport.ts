import { existsSync, lstatSync, mkdirSync, realpathSync } from 'node:fs';
import { dirname, relative, resolve, sep, parse } from 'node:path';
import { stable, type Check, type Spec } from './score';

export type ObjectValue = Record<string, any>;
export type Tool = {id:string;name:string;args:string;result:any;is_error:boolean|null;ended:boolean;elapsed_ms:number};
export type StreamState = {tools:Tool[];terminal:'RUN_FINISHED'|'RUN_ERROR'|null;incomplete:boolean;truncated:boolean;error_code:string|null;usage:CallUsage[];started_at:string;finished_at:string|null; configuration?:{provider:string;model:string}};
const mutations=new Set(['create_dashboard','edit_dashboard','replace_dashboard']);
const decode=(v:any)=>{if(typeof v!=='string')return v;try{return JSON.parse(v)}catch{return null}};
export function originURL(origin:string):URL {
  const url=new URL(origin);
  if(!['http:','https:'].includes(url.protocol)||url.username||url.password||url.pathname!=='/'||url.search||url.hash)throw new Error('Invalid origin');
  return url;
}
function endpoint(origin:string,path:string):URL {
  const base=originURL(origin);
  if(!path.startsWith('/')||path.startsWith('//')||path.includes('\\'))throw new Error('Invalid request path');
  const url=new URL(path,base);
  if(url.origin!==base.origin)throw new Error('Invalid request origin');
  return url;
}
export class HTTPError extends Error {
  constructor(public status:number,public validation_failed=false){super(`HTTP ${status}`)}
}
export async function requestResponse(origin:string,path:string,init:RequestInit={}):Promise<Response> {
  const url=endpoint(origin,path),method=(init.method??'GET').toUpperCase();
  const request={...init,method,redirect:'manual' as const};
  // No retries after a body has been consumed, including malformed JSON or SSE.
  for(let attempt=0;;attempt++) {
    let response:Response;
    try {response=await fetch(url,request)}catch {
      if(method==='GET'&&attempt===0&&!init.signal?.aborted)continue;
      throw new Error(init.signal?.aborted?'Request aborted':'Request disconnected');
    }
    if(method==='GET'&&attempt===0&&response.status>=500) {await response.body?.cancel();continue;}
    if(!response.ok) {
      let validation=false;
      if(response.status===400) {try {const body=await response.json();validation=Array.isArray(body.problems)&&body.problems.length>0;}catch {}}
      else await response.body?.cancel();
      throw new HTTPError(response.status,validation);
    }
    return response;
  }
}
export async function requestJSON(origin:string,path:string,init:RequestInit={}):Promise<any> {
  const response=await requestResponse(origin,path,init);
  try {return await response.json()}catch {throw new Error('Invalid JSON response')}
}
export function cookieHeader(jar:string,url:URL,now=Date.now()/1000):string {
  const matches:{name:string;value:string;path:string}[]=[];
  for(let line of jar.split(/\r?\n/)) {
    if(line.startsWith('#HttpOnly_'))line=line.slice(10);else if(!line||line.startsWith('#'))continue;
    const fields=line.split('\t');if(fields.length!==7)throw new Error('Malformed cookie jar');
    const [rawDomain,subdomains,path,secure,expiry,name,value]=fields;
    if(!Number.isFinite(Number(expiry))||!path.startsWith('/')||!name||/[\r\n;\x00]/.test(name+value))throw new Error('Invalid cookie jar');
    const domain=rawDomain.replace(/^\./,'').toLowerCase(),host=url.hostname.toLowerCase();
    const domainOK=host===domain||(subdomains.toUpperCase()==='TRUE'&&host.endsWith('.'+domain));
    const pathOK=url.pathname===path||(url.pathname.startsWith(path)&&(path.endsWith('/')||url.pathname[path.length]==='/'));
    if(!domainOK||!pathOK||(secure.toUpperCase()==='TRUE'&&url.protocol!=='https:')||(Number(expiry)!==0&&Number(expiry)<=now))continue;
    matches.push({name,value,path});
  }
  return matches.sort((a,b)=>b.path.length-a.path.length).map(c=>`${c.name}=${c.value}`).join('; ');
}

// Consume through EOF: Fanout persists after the terminal event and before closing SSE.
export async function readSSE(response:Response,signal:AbortSignal,onUsage?:(u:CallUsage)=>void):Promise<StreamState> {
  const started=performance.now();
  const state:StreamState={tools:[],terminal:null,incomplete:false,truncated:false,error_code:null,usage:[],started_at:new Date().toISOString(),finished_at:null};
  const fail=(code:string)=>{state.incomplete=true;state.error_code??=code};
  if(!response.body||!response.headers.get('content-type')?.includes('text/event-stream')) {fail('not_sse');return state;}
  const tools=new Map<string,Tool>(),reader=response.body.getReader(),decoder=new TextDecoder('utf-8',{fatal:true});
  let buffer='',data:string[]=[],eventName='',bytes=0;
  const tool=(id:any):Tool=>{if(typeof id!=='string'||!id)throw new Error('invalid_tool_id');if(!tools.has(id)){const t={id,name:'',args:'',result:null,is_error:null,ended:false,elapsed_ms:0};tools.set(id,t);state.tools.push(t);}return tools.get(id)!};
  const event=(e:ObjectValue)=>{
    if(state.terminal)throw new Error('event_after_terminal');
    switch(e.type) {
      case 'TOOL_CALL_START': {const t=tool(e.toolCallId);if(t.name)throw new Error('duplicate_tool_id');if(typeof e.toolCallName!=='string'||!e.toolCallName)throw new Error('missing_tool_name');t.name=e.toolCallName;break;}
      case 'TOOL_CALL_ARGS':tool(e.toolCallId).args+=e.delta??'';break;
      case 'TOOL_CALL_END':tool(e.toolCallId).ended=true;break;
      case 'TOOL_CALL_RESULT': {const t=tool(e.toolCallId);if(t.result!==null)throw new Error('duplicate_tool_result');t.result=structuredClone(decode(e.content));t.elapsed_ms=performance.now()-started;t.is_error=typeof e.isError==='boolean'?e.isError:typeof e.is_error==='boolean'?e.is_error:null;if(t.result?.error||t.result?.isError||t.result?.is_error)t.is_error=true;break;}
      case 'ACTIVITY_SNAPSHOT': {
        if(e.activityType==='mcp-app') {const c=e.content;const matches=state.tools.filter(t=>t.name===c?.tool_name&&stable(decode(t.args))===stable(c?.tool_input));if(matches.length===1&&typeof c.is_error==='boolean')matches[0].is_error=c.is_error;}
        break;
      }
      case 'CUSTOM': {
        if(e.name==='model_configuration')state.configuration=e.value;
        if(e.name==='model_call_usage') {state.usage.push(e.value);onUsage?.(e.value);if(e.value.status==='incomplete'){state.truncated=true;fail('truncated');}}
        break;
      }
      case 'RUN_FINISHED':state.terminal='RUN_FINISHED';break;
      case 'RUN_ERROR':state.terminal='RUN_ERROR';fail('run_error');break;
    }
  };
  const dispatch=()=>{if(data.length){const raw=data.join('\n');if(raw!=='[DONE]'){let e;try{e=JSON.parse(raw)}catch{throw new Error('invalid_sse_json')}if(!e||typeof e!=='object'||Array.isArray(e))throw new Error('invalid_sse_event');event({...e,type:e.type??eventName});}}data=[];eventName='';};
  const line=(s:string)=>{if(!s){dispatch();return;}if(s.startsWith(':'))return;const i=s.indexOf(':'),k=i<0?s:s.slice(0,i);let v=i<0?'':s.slice(i+1);if(v.startsWith(' '))v=v.slice(1);if(k==='data')data.push(v);if(k==='event')eventName=v;};
  const drain=(eof=false)=>{while(true){const i=buffer.search(/[\r\n]/);if(i<0)break;if(buffer[i]==='\r'&&i===buffer.length-1&&!eof)break;const n=buffer[i]==='\r'&&buffer[i+1]==='\n'?2:1;line(buffer.slice(0,i));buffer=buffer.slice(i+n);}if(eof){if(buffer||data.length)throw new Error('partial_sse_frame');}if(buffer.length>16*1024*1024)throw new Error('oversized_sse');};
  const abort=()=>{fail('aborted');void reader.cancel().catch(()=>{})};
  signal.addEventListener('abort',abort,{once:true});
  try {
    if(signal.aborted)abort();
    while(!signal.aborted){const {value,done}=await reader.read();if(done)break;bytes+=value.byteLength;if(bytes>64*1024*1024)throw new Error('oversized_sse');buffer+=decoder.decode(value,{stream:true});drain();}
    buffer+=decoder.decode();drain(true);
  }catch {fail('stream_invalid_or_disconnected')}
  finally {signal.removeEventListener('abort',abort);await reader.cancel().catch(()=>{});reader.releaseLock();state.finished_at=new Date().toISOString();}
  if(!state.terminal)fail('missing_terminal');
  return state;
}
export function findSaved(state:StreamState,messages:ObjectValue[]=[]):{record:ObjectValue;elapsed_ms:number;first_elapsed_ms:number;tool_id:string}|null {
  for(const t of state.tools) {
    const matches=messages.filter(m=>m.role==='tool'&&m.toolCallId===t.id);
    if(matches.length===1)t.is_error=t.is_error===true||Boolean(matches[0].error);
    if(matches.length>1)t.is_error=true;
  }
  const saves=state.tools.filter(t=>mutations.has(t.name)&&t.is_error===false&&t.result?.dashboard?.id&&Number.isInteger(t.result.dashboard.version)&&t.result.dashboard.spec?.panels);
  if(!saves.length)return null;
  // Multiple writes to different dashboards are ambiguous even if the last one looks right.
  if(new Set(saves.map(t=>t.result.dashboard.id)).size!==1)return null;
  const t=saves.at(-1)!;
  return {record:structuredClone(t.result.dashboard),elapsed_ms:t.elapsed_ms,first_elapsed_ms:saves[0].elapsed_ms,tool_id:t.id};
}
export async function executePanels(request:(path:string,init?:RequestInit)=>Promise<any>,spec:Spec,signal:AbortSignal):Promise<{valid:boolean;checked:boolean;checks:Check[]}> {
  try {
    const data=await request('/api/panels/query',{method:'POST',body:JSON.stringify({dashboard:spec}),signal});
    if(!Array.isArray(data.results))throw new Error('missing_checks');
    const checks=data.results.map((r:any)=>({id:r.id,status:r.status,rows:r.frame?.rows??0,...(typeof r.diagnosis==='string'?{diagnosis:r.diagnosis}:{})}));
    if(checks.some((c:Check)=>typeof c.id!=='string'||typeof c.status!=='string'||!Number.isFinite(c.rows)||c.rows<0))throw new Error('invalid_checks');
    const checked=checks.length===spec.panels.length&&new Set(checks.map((c:Check)=>c.id)).size===spec.panels.length&&spec.panels.every(p=>checks.some((c:Check)=>c.id===p.id));
    return {valid:true,checked,checks};
  }catch(e) {return {valid:false,checked:e instanceof HTTPError&&e.validation_failed,checks:[]};}
}

export type Usage={input_tokens:number;output_tokens:number;cache_read_tokens:number;cache_write_tokens:number;reasoning_tokens:number};
export type CallUsage={run_id:string;step:number;provider:string;model:string;status:string;usage:Usage|null};
export type Rate={provider:string;model:string;verified_at:string;input_includes_cache:boolean;input:number;output:number;cache_read:number;cache_write:number};
export type Ledger={schema:1;rates:Rate[];calls:(CallUsage&{cost_usd:number|null})[];prompts:{run_id:string;prompt_id?:string;settled:boolean;completed:boolean;status?:'completed'|'failed';cost_usd:number|null;manual_settlement?:{cost_usd:number;reason:string;settled_at:string}}[]};
function callCost(ledger:Ledger,call:CallUsage):number|null {
  if(!call.usage)return null;
  const rates=ledger.rates.filter(r=>r.provider===call.provider&&r.model===call.model);
  if(rates.length!==1)return null;
  const rate=rates[0],u=call.usage;
  const tokens=[u.input_tokens,u.output_tokens,u.cache_read_tokens,u.cache_write_tokens,u.reasoning_tokens];
  if(!Number.isFinite(Date.parse(rate.verified_at))||typeof rate.input_includes_cache!=='boolean'||[rate.input,rate.output,rate.cache_read,rate.cache_write,...tokens].some(v=>!Number.isFinite(v)||v<0)||tokens.some(v=>!Number.isInteger(v))||u.reasoning_tokens>u.output_tokens)throw new Error('Invalid rates or usage');
  const input=rate.input_includes_cache?u.input_tokens-u.cache_read_tokens-u.cache_write_tokens:u.input_tokens;
  if(input<0)throw new Error('Invalid cached input usage');
  return (input*rate.input+u.output_tokens*rate.output+u.cache_read_tokens*rate.cache_read+u.cache_write_tokens*rate.cache_write)/1_000_000;
}
export function validateLedger(l:Ledger):void {
  if(l?.schema!==1||!Array.isArray(l.rates)||!Array.isArray(l.calls)||!Array.isArray(l.prompts))throw new Error('Invalid cost ledger');
  if(!l.rates.length||new Set(l.rates.map(r=>r.provider+':'+r.model)).size!==l.rates.length||l.rates.some(r=>!r.provider||!r.model||!Number.isFinite(Date.parse(r.verified_at))||typeof r.input_includes_cache!=='boolean'||[r.input,r.output,r.cache_read,r.cache_write].some(v=>!Number.isFinite(v)||v<0)))throw new Error('Verified rates required');
  if(new Set(l.prompts.map(p=>p.run_id)).size!==l.prompts.length)throw new Error('Duplicate ledger prompt');
  const seen=new Set<string>();
  for(const c of l.calls) {
    const key=`${c.run_id}:${c.step}`;
    if(seen.has(key)||!l.prompts.some(p=>p.run_id===c.run_id)||!Number.isInteger(c.step)||c.step<1||c.step>16||!['completed','incomplete','error'].includes(c.status)||!c.provider||!c.model||stable(c.cost_usd)!==stable(callCost(l,c)))throw new Error('Invalid ledger call');
    seen.add(key);
  }
  for(const p of l.prompts) {
    const calls=l.calls.filter(c=>c.run_id===p.run_id),known=calls.reduce((n,c)=>n+(c.cost_usd??0),0);
    if(p.manual_settlement) {
      const m=p.manual_settlement;
      if(!p.settled||p.completed||p.status!=='failed'||!Number.isFinite(m.cost_usd)||m.cost_usd<known||!m.reason?.trim()||!Number.isFinite(Date.parse(m.settled_at))||p.cost_usd!==m.cost_usd)throw new Error('Invalid manual settlement');
    }else if(p.settled && (!calls.length||calls.some(c=>c.cost_usd===null)||p.cost_usd!==known||calls.some((c,i)=>c.step!==i+1)||p.completed&&calls.some(c=>c.status!=='completed')||p.status!==undefined&&p.status!==(p.completed?'completed':'failed')))throw new Error('Invalid prompt settlement');
  }
}
export function spentUSD(l:Ledger):number {
 return l.prompts.reduce((n,p)=>n+Math.max(p.manual_settlement?.cost_usd??0,l.calls.filter(c=>c.run_id===p.run_id).reduce((sum,c)=>sum+(c.cost_usd??0),0)),0);
}
export function canStartPrompt(ledger:Ledger,capUSD:number):boolean {
  try {validateLedger(ledger)}catch {return false}
  if(!Number.isFinite(capUSD)||capUSD<0||ledger.prompts.some(p=>!p.settled))return false;
  const measured=ledger.prompts.filter(p=>p.completed&&p.settled).map(p=>p.cost_usd!).sort((a,b)=>a-b);
  const estimate=measured.length?measured[Math.ceil(measured.length*0.95)-1]:0.60;
  return spentUSD(ledger)+estimate<=capUSD;
}
export function beginPrompt(l:Ledger,run_id:string,prompt_id=run_id):void {
  if(l.prompts.some(p=>!p.settled||p.run_id===run_id))throw new Error('Unsettled or duplicate prompt');
  l.prompts.push({run_id,prompt_id,settled:false,completed:false,cost_usd:null});
}
export function recordUsage(l:Ledger,c:CallUsage):void {
  if(!l.prompts.some(p=>p.run_id===c.run_id&&!p.settled)||!Number.isInteger(c.step)||c.step<1||c.step>16||!['completed','incomplete','error'].includes(c.status)||!c.provider||!c.model)throw new Error('Invalid usage correlation');
  const previous=l.calls.find(x=>x.run_id===c.run_id&&x.step===c.step);
  if(previous) {const {cost_usd,...old}=previous;if(stable(old)!==stable(c))throw new Error('Conflicting usage correlation');return;}
  const cost_usd=callCost(l,c);l.calls.push({...structuredClone(c),cost_usd});
}
export function settlePrompt(l:Ledger,run_id:string,complete:boolean,definitive=true):boolean {
  const p=l.prompts.find(p=>p.run_id===run_id);if(!p)throw new Error('Unknown prompt');
  const calls=l.calls.filter(c=>c.run_id===run_id);
  p.cost_usd=!calls.length||calls.some(c=>c.cost_usd===null)?null:calls.reduce((n,c)=>n+c.cost_usd!,0);
  p.completed=complete&&calls.length>0&&calls.every((c,i)=>c.cost_usd!==null&&c.status==='completed'&&c.step===i+1);
  p.settled=definitive&&p.cost_usd!==null&&calls.every((c,i)=>c.step===i+1);
  if(p.settled)p.status=p.completed?'completed':'failed';
  return p.settled;
}
export function manualSettlement(l:Ledger,prompt_id:string,cost_usd:number,reason:string):void {
 validateLedger(l);
 const matches=l.prompts.filter(p=>!p.settled&&(p.run_id===prompt_id||p.prompt_id===prompt_id));
 if(matches.length!==1||!Number.isFinite(cost_usd)||!reason.trim()||reason.length>1000)throw new Error('Invalid manual settlement');
 const p=matches[0],known=l.calls.filter(c=>c.run_id===p.run_id).reduce((n,c)=>n+(c.cost_usd??0),0);
 if(cost_usd<known)throw new Error('Settlement cannot remove spend');
 p.manual_settlement={cost_usd,reason,settled_at:new Date().toISOString()};p.cost_usd=cost_usd;p.completed=false;p.settled=true;p.status='failed';validateLedger(l);
}

const inside=(root:string,path:string)=>{const rel=relative(root,path);return rel===''||!(rel==='..'||rel.startsWith('..'+sep)||rel.startsWith(sep));};
export function noSymlinks(path:string):string {
  const target=resolve(path);let part=parse(target).root;
  for(const component of target.slice(part.length).split(sep)) {part=resolve(part,component);if(existsSync(part)&&lstatSync(part).isSymbolicLink())throw new Error('Symlinks are forbidden');}
  return target;
}
export function privateFile(path:string):string {const target=noSymlinks(path);if(!lstatSync(target).isFile())throw new Error('Input must be a regular file');return target;}
export function validateOutput(path:string,set:string,worktree=process.cwd(),sealedRoot=process.env.FANOUT_HOLDOUT_OUT_ROOT):string {
  const root=realpathSync(worktree),target=noSymlinks(path),evalRoot=resolve(root,'.superpowers/eval');
  if(set==='benchmark') {if(!inside(evalRoot,target)||target===evalRoot)throw new Error('Benchmark output must be under .superpowers/eval/');}
  else if(set==='holdout') {if(!sealedRoot)throw new Error('Controller output root required');const outRoot=noSymlinks(sealedRoot);if(inside(root,outRoot)||inside(root,target)||!inside(outRoot,target)||target===outRoot)throw new Error('Sealed output must be outside the worktree');}
  else throw new Error('Invalid evaluation set');
  if(existsSync(target))throw new Error('Output already exists');
  return target;
}
export function safeOutput(path:string,set:string,worktree=process.cwd(),sealedRoot=process.env.FANOUT_HOLDOUT_OUT_ROOT):string {
  const target=validateOutput(path,set,worktree,sealedRoot);
  mkdirSync(dirname(target),{recursive:true});noSymlinks(dirname(target));mkdirSync(target);return target;
}
