import { expect, it } from 'vitest';
import { changeLabel, receiptForTurn } from './dashboard-receipt';
import { user, call, result, saved, build } from '../tests/dashboard-receipts';
it('uses signed panel change chips',()=>{
 expect(changeLabel({panel_id:'pool',title:'Pool',kind:'added'})).toBe('+ Pool');
 expect(changeLabel({panel_id:'latency',title:'Latency',kind:'changed',fields:['thresholds']})).toBe('~ latency.thresholds');
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
 messages.push(call('save','replace_dashboard',{dashboard:{panels:[{id:'pool'}]}}),result('save',{...saved,receipt:{...saved.receipt,changes:[{panel_id:'latency',title:'Latency',kind:'removed'}]}}));
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
 expect(changeLabel(receiptForTurn(messages,'second')!.saved!.receipt.changes[0])).toBe('+ <script>Pool</script>');
});
