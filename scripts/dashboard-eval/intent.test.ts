import {expect,it} from 'bun:test';
import {answerPass,scoreIntent,type IntentRun} from './intent';
const answer=():IntentRun=>({expect:'answer',final_text:'p95 is a percentile.',complete:true,
  incomplete:false,saved:false,checked:false,valid:false,elapsed_ms:null,panels:[],checks:[],
  mutation_observed:false,mutation_evidence_complete:true});
it('scores an all-answer set without requiring saves, panels or edits',()=>{
  expect(scoreIntent([answer()],1)).toMatchObject({passed:true,answer_rate:1,dashboard_rate:null,s5:null});
});
it('rejects unintended mutations, blank text, failures and unknown mutation evidence',()=>{
  for(const patch of [{saved:true},{mutation_observed:true},{final_text:' '},{complete:false},
    {incomplete:true},{mutation_evidence_complete:false}])expect(answerPass({...answer(),...patch})).toBe(false);
});
it('does not score a partial set as complete',()=>{expect(scoreIntent([answer()],2).passed).toBe(false)});
it('scores eleven dashboards and five answers with independent denominators and exact checks',()=>{
  const dashboard=():IntentRun=>({...answer(),expect:'dashboard',saved:true,checked:true,valid:true,
    elapsed_ms:50001,panels:[{id:'p'}],checks:[{id:'p',status:'ok',rows:1}]});
  const runs=[...Array.from({length:11},dashboard),...Array.from({length:5},answer)];
  expect(scoreIntent(runs,16)).toMatchObject({passed:true,dashboard_prompts:11,answer_prompts:5,intent_accuracy:1,median_ms:50001,s5:null});
  for(const checks of [[],[{id:'other',status:'ok',rows:1}],[{id:'p',status:'ok',rows:1},{id:'p',status:'ok',rows:1}]]) {
    expect(scoreIntent([{...dashboard(),checks}],1).passed).toBe(false);
  }
  expect(scoreIntent([{...dashboard(),panels:[{id:'p',description:' '}],checks:[{id:'p',status:'empty',rows:0,diagnosis:'No data'}]}],1).passed).toBe(false);
});
