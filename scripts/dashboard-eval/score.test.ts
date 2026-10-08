import { expect, it } from 'bun:test';
import { score, changedPanels, compareEdit, type Run } from './score';
const run = (): Run => ({ complete:true, saved:true, elapsed_ms:45000, valid:true, checked:true, panels:[{id:'latency'}], checks:[{id:'latency',status:'ok',rows:2}] });
const edits = () => Array.from({length:5}, () => ({passed:true,changed_ids:['latency'],expected_ids:['latency']}));
it('requires all ten saves and all five consecutive edits', () => {
  expect(score(Array.from({length:10},run),edits()).passed).toBe(true);
  expect(score(Array.from({length:9},run),edits()).s1).toBe(false);
  expect(score(Array.from({length:10},run),edits().slice(1)).s5).toBe(false);
});
it('never accepts unchecked, unexplained or duplicate panel evidence', () => {
  for(const change of [(r:Run)=>{r.checked=false},(r:Run)=>{r.checks[0].status='empty'},(r:Run)=>{r.checks.push({...r.checks[0]})}]) {
    const runs=Array.from({length:10},run); change(runs[0]); expect(score(runs,edits()).s2).toBe(false);
  }
  const runs=Array.from({length:10},run); runs[0].checks[0]={id:'latency',status:'empty',rows:0,diagnosis:'No matching spans'};
  runs[0].panels[0].description='This service has no spans in the selected period.';
  expect(score(runs,edits()).s2).toBe(true);
  runs[0].panels[0].description=' '; expect(score(runs,edits()).s2).toBe(false);
});
it('fails missing latency and measures a true even-sized median', () => {
  const runs=Array.from({length:10},run); runs[0].elapsed_ms=null; expect(score(runs,edits()).s4).toBe(false);
  runs.forEach((r,i)=>r.elapsed_ms=i<5?40000:50000); expect(score(runs,edits()).median_ms).toBe(45000);
  for(const t of [NaN,Infinity,-1]) {runs[0].elapsed_ms=t;expect(score(runs,edits()).s4).toBe(false);}
});
it('distinguishes unsaved timeout, saved invalid spec and incomplete validation', () => {
  const runs=Array.from({length:10},run); Object.assign(runs[0],{saved:false,valid:false,checked:false,elapsed_ms:null});
  expect(score(runs,edits()).s1).toBe(false); expect(score(runs,edits()).validation_failures).toBe(0);
  runs[0].saved=true;runs[0].checked=true;expect(score(runs,edits()).validation_failures).toBe(1);expect(score(runs,edits()).s3).toBe(false);
  runs[0].valid=true;runs[0].checked=false;expect(score(runs,edits()).s3).toBe(false);
});
it('detects unrelated changes, deletion, addition and reordering in field arrays', () => {
  const a=[{id:'a',query:{by:['service','namespace']}},{id:'b',title:'B'}];
  expect(changedPanels(a,[{...a[0],query:{by:['namespace','service']}},a[1]])).toEqual(['a']);
  expect(changedPanels(a,[{...a[0]},{id:'c'}])).toEqual(['b','c']);
});
const spec=()=>({version:1,name:'Mock',time:{range:'1h'},variables:[],annotations:[],panels:[{id:'a',title:'A',width:4,height:2,grid:{x:0,y:0}},{id:'b',title:'B',grid:{x:4,y:0}}]});
it('records packed remove grids once without a neighbor authored chip', () => {
  const before=spec(),expected=spec();expected.panels.splice(0,1);
  const after=structuredClone(expected);after.panels[0].grid={x:0,y:0};
  expect(compareEdit(before,after,expected,'remove')).toMatchObject({passed:true,changed_ids:['a'],expected_ids:['a'],layout_changed:true});
  after.panels[0].width=8;expect(compareEdit(before,after,expected,'remove').passed).toBe(false);
});
it('keeps physical layout out of authored chips while rejecting unrelated packing', () => {
  const before=spec(),expected=spec();expected.panels[0].title='New';
  const after=structuredClone(expected);after.panels[1].grid={x:0,y:0};
  expect(compareEdit(before,after,expected,'title')).toMatchObject({passed:false,changed_ids:['a'],expected_ids:['a'],layout_changed:true});
});
it('reports relative survivor order without giving inserted neighbors chips', () => {
  const before=spec(),expected=spec();expected.panels.reverse();
  expect(compareEdit(before,expected,expected,'move')).toMatchObject({passed:true,changed_ids:['a','b'],expected_ids:['a','b']});
});
it('rejects title no-ops, unrelated grids, order, metadata and authored changes', () => {
  const before=spec(),expected=spec();expected.panels[0].title='New';
  expect(compareEdit(before,expected,expected,'title').passed).toBe(true);
  expect(compareEdit(before,before,expected,'title').passed).toBe(false);
  for(const change of [(s:any)=>s.panels[1].grid.x=0,(s:any)=>s.panels.reverse(),(s:any)=>s.time.range='2h',(s:any)=>s.variables.push({id:'v'}),(s:any)=>s.annotations.push('x'),(s:any)=>s.name='Other',(s:any)=>s.panels[1].width=12]) {
    const after=structuredClone(expected);change(after);expect(compareEdit(before,after,expected,'title').passed).toBe(false);
  }
});
it('includes text panels, rejects extra checks and duplicate saved IDs', () => {
  const runs=Array.from({length:10},run);runs[0].panels.push({id:'text'});
  expect(score(runs,edits()).s2).toBe(false);
  runs[0].checks.push({id:'text',status:'ok',rows:1});expect(score(runs,edits()).s2).toBe(true);
  runs[0].panels[1].id='latency';expect(score(runs,edits()).s2).toBe(false);
});
