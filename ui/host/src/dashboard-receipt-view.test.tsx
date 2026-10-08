import { createRootRoute, createRoute, createRouter, RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import type { BuildReceipt } from "./dashboard-receipt";
import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { DashboardReceiptView } from "./dashboard-receipt-view";
import { receiptForTurn } from "./dashboard-receipt";
import { build, call, user, result, saved } from "../tests/dashboard-receipts";

function view(receipt:BuildReceipt, running=false) {
 const route=createRootRoute({component:()=> <DashboardReceiptView receipt={receipt} running={running} awaitingAnswer={running}/>});
 const dashboard=createRoute({getParentRoute:()=>route,path:'/dashboards/$dashboardId',component:()=>null});
 const router=createRouter({routeTree:route.addChildren([dashboard]),history:createMemoryHistory({initialEntries:['/']})});
 return <MantineProvider><RouterProvider router={router}/></MantineProvider>;
}
it('renders one quiet status, safe chips, exact saved-version link and keyboard details', async () => {
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host);
 try {
  await act(async()=>root.render(view(receiptForTurn(build(),'u')!)));
  expect(host.querySelectorAll('[role="status"]')).toHaveLength(1);
  expect(host.textContent).toContain('Read telemetry · 1 panel · Checked in 1.2 s · Saved v2');
  expect(host.textContent).toContain('~ latency.thresholds');expect(host.textContent).toContain('Layout adjusted');
  expect(host.querySelector('img')).toBeNull();
  const link=host.querySelector('a')!;expect(link.getAttribute('href')).toBe('/dashboards/board');expect(link.textContent).toBe('Open dashboard');
  const details=host.querySelector<HTMLButtonElement>('button[aria-expanded]')!;expect(details.getAttribute('aria-expanded')).toBe('false');
  details.focus();expect(document.activeElement).toBe(details);
  await act(async()=>details.click());expect(details.getAttribute('aria-expanded')).toBe('true');
  expect(host.textContent?.match(/(?:Saved v|saved version |saved v)2/g)).toHaveLength(1);
  expect(host.textContent).toContain('Unknown field');expect(host.textContent).toContain('Preview');
 }finally{await act(async()=>root.unmount());host.remove();}
});
it('keeps unchecked/error/empty explanations visible with details collapsed', async()=>{
 const messages=build();messages[messages.length-1]=result('save',{...saved,receipt:{...saved.receipt,save_check:{checked:false,reason:'Timed out',elapsed_ms:8000,panels:[{id:'latency',status:'empty',rows:0,diagnosis:'No matching events'}]}}});
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host);
 try{await act(async()=>root.render(view(receiptForTurn(messages,'u')!)));
 expect(host.textContent).toContain('Timed out');expect(host.textContent).toContain('No matching events');expect(host.textContent).not.toContain('Checked in');
 expect(host.querySelector('button[aria-expanded]')?.getAttribute('aria-expanded')).toBe('false');
 }finally{await act(async()=>root.unmount());host.remove();}
});

it('shows in-flight stages as progress while keeping observed failures visible',async()=>{
 const messages=build();messages.pop();const receipt=receiptForTurn(messages,'u')!;
 receipt.stages.context.state='failed';receipt.explanations=['context: failed · save: incomplete'];
 const host=document.createElement('div');const root=createRoot(host);
 try{await act(async()=>root.render(view(receipt,true)));
 expect(host.querySelector('[data-build-running]')).not.toBeNull();
 expect(host.querySelector('[data-receipt-attention]')?.textContent).toBe('context: failed');
 await act(async()=>host.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click());
 expect(host.textContent).toContain('Save: in progress');expect(host.textContent).not.toContain('save: incomplete');
 }finally{await act(async()=>root.unmount());}
});

it.each(['create_dashboard','edit_dashboard'])('renders interrupted %s with muted outcome-unknown text',async name=>{
 const receipt=receiptForTurn([user(),call('save',name),result('save',{error:'interrupted'},'interrupted')],'u')!;
 const host=document.createElement('div');const root=createRoot(host);
 try{await act(async()=>root.render(view(receipt)));
 const notice=Array.from(host.querySelectorAll<HTMLElement>('[data-receipt-attention]')).find(el=>el.textContent==='Save interrupted · outcome unknown');
 expect(notice).toBeDefined();expect(notice!.style.color).toBe('var(--mantine-color-dimmed)');expect(notice!.getAttribute('role')).not.toBe('alert');
 expect(host.textContent).not.toMatch(/Save failed|not saved/);
 await act(async()=>host.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click());expect(host.textContent).toContain('Save: interrupted');
 }finally{await act(async()=>root.unmount());}
});
it('lists a failed attempt as retried in Details beside the proven save',async()=>{
 const receipt=receiptForTurn([user(),call('failed','edit_dashboard'),result('failed',{error:'stale'}),call('retry','edit_dashboard'),result('retry',saved)],'u')!;
 const host=document.createElement('div');const root=createRoot(host);
 try{await act(async()=>root.render(view(receipt)));expect(host.textContent).toContain('Saved v2');
 await act(async()=>host.querySelector<HTMLButtonElement>('button[aria-expanded]')!.click());
 expect(host.textContent).toContain('Save attempt 1: retried');expect(host.textContent).toContain('Save: complete');expect(host.textContent).not.toContain('Save: failed');
 }finally{await act(async()=>root.unmount());}
});
