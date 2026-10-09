import { expect, it } from 'vitest';
import { changeChips, receiptForTurn } from './dashboard-receipt';
import { user, call, result, saved, build } from '../tests/dashboard-receipts';
it('uses signed panel change chips',()=>{
 expect(changeChips({changes:[{panel_id:'pool',title:'Pool',kind:'added'}],layout_changed:false})).toEqual(['+ Pool']);
 expect(changeChips({changes:[{panel_id:'latency',title:'Latency',kind:'changed',fields:['thresholds']}],layout_changed:false})).toEqual(['~ latency.thresholds']);
});
it('reconstructs observed stages, corrections, timings and exact saved receipt on reload',()=>{
 const messages=build(); const receipt=receiptForTurn(messages,'u')!;
 expect(receipt.panel_count).toBe(1); expect(receipt.saved).toMatchObject({id:'board',version:2,receipt:saved.receipt});
 expect(receipt.corrections).toEqual([{panel_id:'latency',path:'panels[0].query.measures[0]',message:'Unknown field'}]);
 expect(receipt.stages.preview).toMatchObject({state:'complete',elapsed_ms:200});
 expect(receipt.panels).toMatchObject([{id:'latency',status:'ok'}]);
 expect(receiptForTurn(JSON.parse(JSON.stringify(messages)),'u')).toEqual(receipt);
});
it('ignores narration, reads, mismatched call IDs and other user turns',()=>{
 expect(receiptForTurn([user(),call('read','get_dashboard'),result('read',saved),{id:'text',role:'assistant',content:'create_dashboard saved v2'}],'u')).toBeNull();
 const messages=[user(),call('save','create_dashboard'),result('other',saved),user('next'),result('save',saved)];
 expect(receiptForTurn(messages,'u')?.saved).toBeUndefined();
 expect(receiptForTurn(messages,'next')).toBeNull();
});
it('keeps failed and interrupted saves incomplete without inventing success',()=>{
 const messages=[user(),call('save','create_dashboard'),result('save',{error:'failed'},'error')];
 expect(receiptForTurn(messages,'u')?.stages.save.state).toBe('failed');
 expect(receiptForTurn([user(),call('save','create_dashboard')],'u')?.stages.save.state).toBe('incomplete');
});
it('labels removals, requires later successful previews and keeps parallel panels separate',()=>{
 const messages=build().slice(0,5);
 messages.push(call('other','preview_panels',{panels:[{id:'pool'}]}),result('other',{elapsed_ms:12,panels:[{id:'pool',status:'ok'}]}));
 expect(receiptForTurn(messages,'u')?.corrections).toEqual([]);
 messages.push(call('save','replace_dashboard',{dashboard:{panels:[{id:'pool'}]}}),result('save',{...saved,receipt:{...saved.receipt,changes:[{panel_id:'latency',title:'Latency',kind:'removed'}],save_check:{...saved.receipt.save_check,panels:[{id:'pool',status:'ok',rows:1}]}}}));
 expect(receiptForTurn(messages,'u')?.corrections).toEqual([]);
 expect(receiptForTurn(messages,'u')?.saved?.receipt.changes[0].kind).toBe('removed');
});
it('separates an unchecked post-save check from the earlier successful preview',()=>{
 const messages=build(); messages[messages.length-1]=result('save',{...saved,receipt:{...saved.receipt,save_check:{checked:false,reason:'Timed out',elapsed_ms:8000,panels:[{id:'latency',status:'not_run',rows:0}]}}});
 expect(receiptForTurn(messages,'u')?.saved?.receipt.save_check.checked).toBe(false);
 expect(receiptForTurn(messages,'u')?.stages.preview.state).toBe('complete');
});
it('observes app telemetry context from matched successful summary results',()=>{
 const messages=build();messages.splice(3,0,call('context','get_service_topology'),{id:'context-result',role:'tool',toolCallId:'context',content:'Observed 2 services.'});
 expect(receiptForTurn(messages,'u')?.stages.context.state).toBe('complete');
});
it('does not label a parallel pending or invalid panel checked when another preview succeeds',()=>{
 const messages=build().slice(0,5);messages.push(call('pending','preview_panels',{panels:[{id:'pending'}]}),call('pool','preview_panels',{panels:[{id:'pool'}]}),result('pool',{elapsed_ms:12,panels:[{id:'pool',status:'ok'}]}));
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.stages.validation.state).not.toBe('complete');expect(receipt.stages.preview.state).toBe('incomplete');
 expect(receipt.panels.find(p=>p.id==='pending')?.status).toBe('not_run');
});
it('keeps separate receipts across build turns and uses create chips only from the server',()=>{
 const messages=build();messages.push(user('second'),call('create','create_dashboard',{dashboard:{panels:[{id:'pool'}]}}),result('create',{dashboard:{id:'new',name:'New',version:1},receipt:{base_version:0,version:1,changes:[{panel_id:'pool',title:'<script>Pool</script>',kind:'added'}],layout_changed:false,save_check:{checked:true,elapsed_ms:20,panels:[{id:'pool',status:'ok',rows:1}]}}}));
 expect(receiptForTurn(messages,'u')?.saved?.id).toBe('board');expect(receiptForTurn(messages,'second')?.saved?.version).toBe(1);
 expect(changeChips(receiptForTurn(messages,'second')!.saved!.receipt)[0]).toBe('+ <script>Pool</script>');
});
it.each(['empty','error'])('records a correction when validation resolves into %s',status=>{
 const messages=build().slice(0,5);messages.push(call('fixed','preview_panels',{panels:[{id:'latency'}]}),result('fixed',{elapsed_ms:20,panels:[{id:'latency',status,diagnosis:'No matching events'}]}));
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.problems).toEqual([]);expect(receipt.corrections).toHaveLength(1);expect(receipt.stages.validation.state).toBe('complete');expect(receipt.stages.preview.state).toBe('complete');expect(receipt.explanations.join(' ')).toContain(status);
});
it('records a correction from the committed save check of the same panel',()=>{
 const messages=build().slice(0,5);messages.push(call('save','edit_dashboard'),result('save',saved));
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.corrections).toHaveLength(1);expect(receipt.problems).toEqual([]);expect(receipt.stages.validation.state).toBe('complete');expect(receipt.stages.preview.state).toBe('incomplete');
});
it('keeps the committed version when a later change fails, and clears failure on a successful retry',()=>{
 const messages=build();messages.push(call('failed','edit_dashboard'),result('failed',{error:'stale'}));
 let receipt=receiptForTurn(messages,'u')!;expect(receipt.saved?.version).toBe(2);expect(receipt.explanations.join(' ')).toContain('A later change failed; v2 remains saved');expect(receipt.explanations.join(' ')).not.toContain('dashboard was not saved');
 messages.push(call('retry','edit_dashboard'),result('retry',{...saved,dashboard:{...saved.dashboard,version:3},receipt:{...saved.receipt,base_version:2,version:3,changes:[]}}));
 receipt=receiptForTurn(messages,'u')!;expect(receipt.saved?.version).toBe(3);expect(receipt.explanations.join(' ')).not.toContain('failed');
});
it('renders no receipt for an undecodable committed save without inventing failure',()=>{
 expect(receiptForTurn([user(),call('old','create_dashboard'),result('old',{dashboard:{id:'old',name:'Old',version:1}})],'u')).toBeNull();
 expect(receiptForTurn([user(),call('drift','create_dashboard'),result('drift',{dashboard:saved.dashboard,receipt:{unsupported:true}})],'u')).toBeNull();
});
it.each([
 {first:'added',next:'changed',expected:'added'},
 {first:'added',next:'removed',expected:undefined},
 {first:'changed',next:'removed',expected:'removed'},
] as const)('accumulates net panel chips for $first then $next',({first,next,expected})=>{
 const mutation=(kind:string,version:number)=>({...saved,dashboard:{...saved.dashboard,version},receipt:{...saved.receipt,base_version:first==='added'&&version===1?0:version-1,version,changes:[{panel_id:'latency',title:'Latency',kind,fields:kind==='changed'?['thresholds']:undefined}]}});
 const messages=[user(),call('first',first==='added'?'create_dashboard':'edit_dashboard'),result('first',mutation(first,1)),call('next','edit_dashboard'),result('next',mutation(next,2))];
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.saved!.receipt.changes).toEqual(expected?[expect.objectContaining({kind:expected})]:[]);expect(receipt.saved!.receipt.base_version).toBe(0);if(expected==='added')expect(receipt.saved!.label).toBe('Created');
});

it('records resolved validation paths even when a later preview finds another problem',()=>{
 const messages=build().slice(0,5);messages.push(call('next','preview_panels',{panels:[{id:'latency'}]}),result('next',{elapsed_ms:20,panels:[{id:'latency',status:'invalid',problems:[{path:'query.sort',message:'Invalid sort'}]}]}));
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.corrections).toHaveLength(1);expect(receipt.problems).toEqual([{panel_id:'latency',path:'query.sort',message:'Invalid sort'}]);expect(receipt.stages.validation.state).toBe('failed');
});

it.each(['create_dashboard','edit_dashboard'])('keeps an interrupted %s outcome unknown after reload',name=>{
 const messages=[user(),call('save',name),result('save',{error:{code:'interrupted',message:'Tool execution was interrupted. The save may have completed.'}},'interrupted')];
 const receipt=receiptForTurn(messages,'u')!;
 expect(receipt.stages.save.state).toBe('interrupted');expect(receipt.saved).toBeUndefined();
 expect(receipt.explanations).toContain('Save interrupted · outcome unknown');
 expect(receipt.explanations.join(' ')).not.toMatch(/failed|not saved/);
 expect(receiptForTurn(JSON.parse(JSON.stringify(messages)),'u')).toEqual(receipt);
});
it.each(['same call','later call'])('uses proven success after an interrupted save (%s)',mode=>{
 const messages=[user(),call('save','edit_dashboard'),result('save',{error:{code:'interrupted',message:'Tool execution was interrupted. The save may have completed.'}},'interrupted')];
 if(mode==='later call') messages.push(call('retry','edit_dashboard'));
 messages.push({...result(mode==='same call'?'save':'retry',saved),id:'proven-result'});
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.stages.save.state).toBe('complete');expect(receipt.saved?.version).toBe(2);
 expect(receipt.explanations.join(' ')).not.toMatch(/interrupted|failed|outcome unknown/);
});
it('marks an observed failed attempt retried after a successful save',()=>{
 const messages=[user(),call('failed','edit_dashboard'),result('failed',{error:'stale'}),call('retry','edit_dashboard'),result('retry',saved)];
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.stages.save.state).toBe('complete');
 expect(receipt.save_attempts).toEqual([{call_id:'failed',state:'retried'},{call_id:'retry',state:'complete'}]);
});
it('keeps the last proven version when a later save is interrupted',()=>{
 const messages=build();messages.push(call('next','edit_dashboard'),result('next',{error:{code:'interrupted',message:'Tool execution was interrupted. The save may have completed.'}},'interrupted'));
 const receipt=receiptForTurn(messages,'u')!;expect(receipt.saved?.version).toBe(2);expect(receipt.stages.save.state).toBe('interrupted');expect(receipt.explanations).toContain('Save interrupted · outcome unknown');expect(receipt.explanations.join(' ')).not.toContain('failed');
});

it('uses the same committed restore receipt for live and reloaded turns',()=>{
 const messages=[user(),call('restore','restore_dashboard_version',{id:'board',version:1}),result('restore',saved)];
 const receipt=receiptForTurn(messages,'u')!;
 expect(receipt.saved).toMatchObject({id:'board',version:2,label:'Restored',receipt:saved.receipt});
 expect(receipt.stages.save.state).toBe('complete');
 expect(receiptForTurn(JSON.parse(JSON.stringify(messages)),'u')).toEqual(receipt);
});
it.each(['error','failed','invalid','conflict','stale','denied','required','interrupted','cancelled','canceled','timeout','timed out','not found'])('does not infer a mutation error from free text containing %s',text=>{
 for(const name of ['create_dashboard','edit_dashboard','replace_dashboard','restore_dashboard_version']) {
  const messages=[user(),call('save',name),{id:'r-save',role:'tool' as const,toolCallId:'save',content:`Summary: ${text}`}];
  expect(receiptForTurn(messages,'u')).toBeNull();
 }
});
it.each(['create_dashboard','restore_dashboard_version'])('reads %s errors only from structured fields',name=>{
 for(const message of [result('save',{error:'denied'}),result('save',{isError:true}),{id:'r-save',role:'tool' as const,toolCallId:'save',content:'Denied',error:'failed'}]) {
  const messages=[user(),call('save',name),message];
  expect(receiptForTurn(messages,'u')?.stages.save.state).toBe('failed');
  expect(receiptForTurn(JSON.parse(JSON.stringify(messages)),'u')?.stages.save.state).toBe('failed');
 }
});

it("omits a save receipt for structural answer-only refusals",()=>{
 expect(receiptForTurn([user(),call("refused","create_dashboard"),result("refused",{error:{code:"answer_only",message:"Read-only"},isError:true},"Read-only")],"u")).toBeNull();
});
