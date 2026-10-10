export type SavedPanel = { id:string; description?:string; viz?:string; content?:string };
export type Check = { id:string; status:string; rows:number; diagnosis?:string; error?:string };
export type Run = {
  complete:boolean; saved:boolean; elapsed_ms:number|null; valid:boolean; checked:boolean;
  panels:SavedPanel[]; checks:Check[];
};
export type Edit = { passed:boolean; changed_ids:string[]; expected_ids:string[]; layout_changed?:boolean };
export function median(xs:number[]):number|null {
  if (!xs.length) return null;
  const sorted=[...xs].sort((a,b)=>a-b), i=Math.floor(sorted.length/2);
  return sorted.length%2 ? sorted[i] : (sorted[i-1]+sorted[i])/2;
}
export function panelPass(p:SavedPanel, checks:Check[]):boolean {
  const matches=checks.filter(c=>c.id===p.id);
  if(matches.length!==1)return false;
  const c=matches[0];
  return c.status==='ok' && (c.rows>0 || p.viz==='text' && Boolean(p.content?.trim())) || c.status==='empty' && Boolean(c.diagnosis?.trim()) && Boolean(p.description?.trim());
}
export function score(runs:Run[], edits:Edit[]) {
  const complete=runs.length===10 && runs.every(r=>r.complete);
  const times=runs.map(r=>r.elapsed_ms);
  const latency=times.every(t=>t!==null && Number.isFinite(t) && t>=0) ? median(times as number[]) : null;
  const total=runs.reduce((n,r)=>n+r.panels.length,0);
  const good=runs.reduce((n,r)=>n+(r.checked ? r.panels.filter(p=>panelPass(p,r.checks)).length : 0),0);
  const validation_failures=runs.filter(r=>r.saved && r.checked && !r.valid).length;
  const s1=complete && runs.every(r=>r.saved);
  const s2=complete && total>0 && good===total && runs.every(r=>r.checked && r.panels.length>0 && r.checks.length===r.panels.length && new Set(r.panels.map(p=>p.id)).size===r.panels.length);
  // Checked validation is required even when the caller claims valid=true.
  const s3=complete && runs.every(r=>r.saved && r.checked) && validation_failures===0;
  const s4=complete && latency!==null && latency<=45000;
  const s5=edits.length===5 && edits.every(e=>e.passed && stable([...e.changed_ids].sort())===stable([...e.expected_ids].sort()));
  return {s1,s2,s3,s4,s5,passed:s1&&s2&&s3&&s4&&s5,saved:runs.filter(r=>r.saved).length,total,good,validation_failures,median_ms:latency};
}
export function stable(value:unknown):string {
  const sort=(v:unknown):unknown => Array.isArray(v)?v.map(sort):v!==null && typeof v==='object' ? Object.fromEntries(Object.entries(v).sort(([a],[b])=>a.localeCompare(b)).map(([k,x])=>[k,sort(x)])) : v;
  return JSON.stringify(sort(value));
}
export function changedPanels(before:{id:string}[],after:{id:string}[]):string[] {
  const strip=(p:{id:string})=>Object.fromEntries(Object.entries(p).filter(([key])=>key!=='grid'));
  const a=new Map(before.map(p=>[p.id,stable(strip(p))])),b=new Map(after.map(p=>[p.id,stable(strip(p))]));
  const oldOrder=before.filter(p=>b.has(p.id)).map(p=>p.id),nextOrder=after.filter(p=>a.has(p.id)).map(p=>p.id);
  const lengths=Array.from({length:oldOrder.length+1},()=>Array(nextOrder.length+1).fill(0));
  for(let i=oldOrder.length-1;i>=0;i--)for(let j=nextOrder.length-1;j>=0;j--)lengths[i][j]=oldOrder[i]===nextOrder[j]?1+lengths[i+1][j+1]:Math.max(lengths[i+1][j],lengths[i][j+1]);
  const retained=new Set<string>();
  // Independently recompute the LCS, retaining earlier after-indices on ties.
  for(let i=0,j=0;i<oldOrder.length&&j<nextOrder.length;){
    if(oldOrder[i]===nextOrder[j]){retained.add(oldOrder[i]);i++;j++;}
    else if(lengths[i+1][j]>=lengths[i][j+1])i++;else j++;
  }
  return [...new Set([...a.keys(),...b.keys()])].filter(id=>a.get(id)!==b.get(id) || a.has(id)&&b.has(id)&&!retained.has(id)).sort();
}
export type Spec = Record<string, any> & {panels:({id:string}&Record<string,any>)[]};
export type Operation = 'title'|'threshold'|'add'|'remove'|'unit'|'move';
export function compareEdit(before:Spec, after:Spec, expected:Spec, operation:Operation):Edit & {expected_diff:unknown;actual_diff:unknown;layout_diff:unknown} {
  const allowGrid=['add','remove','move'].includes(operation);
  const normalized=(s:Spec)=>({...s,panels:s.panels.map(p=>Object.fromEntries(Object.entries(p).filter(([k])=>k!=='grid')))});
  const physical=(s:Spec)=>s.panels.filter(p=>p.grid!=null).map(p=>({id:p.id,grid:p.grid})).sort((a,b)=>a.id.localeCompare(b.id));
  const changed_ids=changedPanels(before.panels,after.panels);
  const expected_ids=changedPanels(before.panels,expected.panels);
  const layout_changed=stable(physical(before))!==stable(physical(after));
  // Added panels may acquire only the server's documented defaults. Existing
  // panels and all explicitly authored fields remain exact comparisons.
  const comparable=structuredClone(normalized(after));
  if(operation==='add')for(const actual of comparable.panels){
    if(before.panels.some(p=>p.id===actual.id))continue;
    const authored=expected.panels.find(p=>p.id===actual.id);
    if(!authored)continue;
    for(const [key,value] of Object.entries(panelDefaults(authored))) {
      if(!(key in authored) && stable(actual[key])===stable(value))delete actual[key];
    }
    if(authored.query && !('bucket' in authored.query) && actual.query?.bucket==='auto' && bucketViz.has(authored.viz))delete actual.query.bucket;
  }
  const authoredEqual=stable(comparable)===stable(normalized(expected));
  // A move has a specific requested placement; other placement operations may pack neighbors.
  const moveOK=operation!=='move' || expected.panels.every(p=>stable(p.grid)===stable(after.panels.find(a=>a.id===p.id)?.grid));
  const noOp=stable(normalized(before))===stable(normalized(expected)) && !(operation==='move' && stable(physical(before))!==stable(physical(expected)));
  return {passed:!noOp && authoredEqual && (allowGrid || !layout_changed) && moveOK && stable(changed_ids)===stable(expected_ids),changed_ids,expected_ids,layout_changed,
    layout_diff:{before:physical(before),expected:physical(expected),actual:physical(after)},
    expected_diff:{changed_ids:expected_ids,before:normalized(before),after:normalized(expected)},actual_diff:{changed_ids,before:normalized(before),after:normalized(after)}};
}

const bucketViz=new Set(['timeseries','heatmap','log_patterns','state_timeline']);
export function panelDefaults(p:Record<string,any>):Record<string,any> {
 const sizes:Record<string,[number,string]>={stat:[3,'s'],gauge:[3,'s'],timeseries:[6,'m'],bar:[6,'m'],table:[12,'m'],text:[4,'s'],service_map:[12,'l'],health:[12,'m'],logs:[12,'l'],log_patterns:[12,'m'],traces:[12,'m'],heatmap:[6,'m'],histogram:[6,'m'],scatter:[6,'m'],state_timeline:[6,'m']};
 const size=sizes[p.viz];return size?{width:size[0],height:size[1],...(['stat','gauge'].includes(p.viz)?{reduce:'window'}:{})}:{};
}
export function normalizeAddedPanel(p:Record<string,any>):any {
 const out={...panelDefaults(p),...structuredClone(p)};
 if(out.query && !('bucket' in out.query) && bucketViz.has(out.viz))out.query.bucket='auto';
 return out;
}
