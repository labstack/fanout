export type SavedPanel = { id:string; description?:string };
export type Check = { id:string; status:string; rows:number; diagnosis?:string };
export type Run = {
  saved:boolean; elapsed_ms:number|null; valid:boolean; checked:boolean;
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
  return c.status==='ok' && c.rows>0 || c.status==='empty' && Boolean(c.diagnosis?.trim()) && Boolean(p.description?.trim());
}
export function score(runs:Run[], edits:Edit[]) {
  const complete=runs.length===10;
  const times=runs.map(r=>r.elapsed_ms);
  const latency=times.every(t=>t!==null && Number.isFinite(t) && t>=0) ? median(times as number[]) : null;
  const total=runs.reduce((n,r)=>n+r.panels.length,0);
  const good=runs.reduce((n,r)=>n+(r.checked ? r.panels.filter(p=>panelPass(p,r.checks)).length : 0),0);
  const validation_failures=runs.filter(r=>r.saved && !r.valid).length;
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
export function changedPanels(before:{id:string}[],after:{id:string}[],allowGrid:boolean):string[] {
  const strip=(p:{id:string})=>allowGrid ? Object.fromEntries(Object.entries(p).filter(([key])=>key!=='grid')) : p;
  const a=new Map(before.map(p=>[p.id,stable(strip(p))])),b=new Map(after.map(p=>[p.id,stable(strip(p))]));
  return [...new Set([...a.keys(),...b.keys()])].filter(id=>a.get(id)!==b.get(id)).sort();
}
export type Spec = Record<string, any> & {panels:({id:string}&Record<string,any>)[]};
export type Operation = 'title'|'threshold'|'add'|'remove'|'unit'|'move';
export function compareEdit(before:Spec, after:Spec, expected:Spec, operation:Operation):Edit & {expected_diff:unknown;actual_diff:unknown;layout_diff:unknown} {
  const allowGrid=['add','remove','move'].includes(operation);
  const normalized=(s:Spec)=>({...s,panels:s.panels.map(p=>allowGrid ? Object.fromEntries(Object.entries(p).filter(([k])=>k!=='grid')) : p)});
  const physical=(s:Spec)=>s.panels.map(p=>({id:p.id,grid:p.grid}));
  const changed_ids=changedPanels(before.panels,after.panels,allowGrid);
  const expected_ids=changedPanels(before.panels,expected.panels,allowGrid);
  const layout_changed=stable(physical(before))!==stable(physical(after));
  const authoredEqual=stable(normalized(after))===stable(normalized(expected));
  // A move has a specific requested placement; other placement operations may pack neighbors.
  const moveOK=operation!=='move' || expected.panels.every(p=>stable(p.grid)===stable(after.panels.find(a=>a.id===p.id)?.grid));
  const noOp=stable(normalized(before))===stable(normalized(expected)) && !(operation==='move' && stable(physical(before))!==stable(physical(expected)));
  return {passed:!noOp && authoredEqual && moveOK && stable(changed_ids)===stable(expected_ids),changed_ids,expected_ids,layout_changed,
    layout_diff:{before:physical(before),expected:physical(expected),actual:physical(after)},
    expected_diff:{changed_ids:expected_ids,before:normalized(before),after:normalized(expected)},actual_diff:{changed_ids,before:normalized(before),after:normalized(after)}};
}
