import { expect,it } from 'vitest';
import { dashboardToolResult } from './dashboard-tool-result';
import { build, call, result, saved, user } from '../tests/dashboard-receipts';
it('requires committed receipt and joins mutation results within the same user turn',()=>{
 expect(dashboardToolResult('save',JSON.stringify(saved),build())).toMatchObject({id:'board',version:2,label:'Updated',receipt:saved.receipt});
 for (const payload of [{dashboard:saved.dashboard},{...saved,receipt:{...saved.receipt,version:3}},{...saved,error:'failed'}]) expect(dashboardToolResult('save',JSON.stringify(payload),build())).toBeNull();
 expect(dashboardToolResult('read',JSON.stringify(saved),[user(),call('read','get_dashboard'),result('read',saved)])).toBeNull();
 expect(dashboardToolResult('save',JSON.stringify(saved),[user(),call('save','create_dashboard'),user('next'),result('save',saved)])).toBeNull();
});

it('recognizes the committed restore receipt live and after reload',()=>{
 const messages=[user(),call('restore','restore_dashboard_version',{id:'board',version:1}),result('restore',saved)];
 const expected={id:'board',version:2,label:'Restored',receipt:saved.receipt};
 expect(dashboardToolResult('restore',messages[2].content,messages)).toMatchObject(expected);
 const reloaded=JSON.parse(JSON.stringify(messages));
 expect(dashboardToolResult('restore',reloaded[2].content,reloaded)).toMatchObject(expected);
});
it('rejects failed, cross-turn and record-only restore results',()=>{
 for(const payload of [{dashboard:saved.dashboard},{...saved,error:'denied'},{...saved,isError:true}]) {
  const messages=[user(),call('restore','restore_dashboard_version'),result('restore',payload)];
  expect(dashboardToolResult('restore',messages[2].content,messages)).toBeNull();
 }
 const failed=[user(),call('restore','restore_dashboard_version'),result('restore',saved,'failed')];
 expect(dashboardToolResult('restore',failed[2].content,failed)).toBeNull();
 const crossed=[user(),call('restore','restore_dashboard_version'),user('next'),result('restore',saved)];
 expect(dashboardToolResult('restore',crossed[3].content,crossed)).toBeNull();
});

it('rejects incomplete or duplicate post-save panel checks',()=>{
 for(const panels of [[{id:'latency',status:'ok'}],[saved.receipt.save_check.panels[0],saved.receipt.save_check.panels[0]]]) {
  const payload={...saved,receipt:{...saved.receipt,save_check:{...saved.receipt.save_check,panels}}};
  expect(dashboardToolResult('save',JSON.stringify(payload),build())).toBeNull();
 }
});
