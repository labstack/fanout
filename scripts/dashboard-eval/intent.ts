import {median,panelPass,type Run} from './score';
export type IntentRun=Run & {expect:'dashboard'|'answer';final_text:string;
  mutation_observed:boolean;mutation_evidence_complete:boolean;incomplete:boolean};
export function answerPass(r:IntentRun):boolean {
  return r.complete&&!r.incomplete&&Boolean(r.final_text.trim())&&!r.saved&&
    !r.mutation_observed&&r.mutation_evidence_complete;
}
export function scoreIntent(runs:IntentRun[],expected_count:number){
  const complete=runs.length===expected_count&&expected_count>0&&runs.every(r=>r.complete&&!r.incomplete);
  const dashboards=runs.filter(r=>r.expect==='dashboard'),answers=runs.filter(r=>r.expect==='answer');
  const saved=dashboards.filter(r=>r.saved&&r.checked&&r.valid&&r.panels.length>0);
  const correct_answers=answers.filter(answerPass).length;
  const total=saved.reduce((n,r)=>n+r.panels.length,0);
  const good=saved.reduce((n,r)=>n+r.panels.filter(p=>panelPass(p,r.checks)).length,0);
  const correct_outcomes=saved.length+correct_answers;
  const times=saved.map(r=>r.elapsed_ms);
  const latency=times.every(t=>t!==null&&Number.isFinite(t)&&t>=0)?median(times as number[]):null;
  const fully_checked=dashboards.every(r=>r.saved&&r.checked&&r.valid&&r.panels.length>0&&
    r.checks.length===r.panels.length&&new Set(r.checks.map(c=>c.id)).size===r.panels.length&&
    new Set(r.panels.map(p=>p.id)).size===r.panels.length&&r.panels.every(p=>r.checks.some(c=>c.id===p.id)));
  return {complete,dashboard_prompts:dashboards.length,answer_prompts:answers.length,
    correct_outcomes,dashboard_rate:dashboards.length?saved.length/dashboards.length:null,
    answer_rate:answers.length?correct_answers/answers.length:null,
    intent_accuracy:runs.length?correct_outcomes/runs.length:null,
    good,total,median_ms:latency,s5:null,
    validation_failures:dashboards.filter(r=>r.saved&&r.checked&&!r.valid).length,
    validation_unchecked:dashboards.filter(r=>r.saved&&!r.checked).length,
    checked_dashboards:dashboards.filter(r=>r.saved&&r.checked).length,
    passed:complete&&correct_outcomes===expected_count&&fully_checked&&good===total};
}
