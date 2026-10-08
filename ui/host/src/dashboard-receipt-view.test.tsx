import { createRootRoute, createRoute, createRouter, RouterProvider, createMemoryHistory } from "@tanstack/react-router";
import type { BuildReceipt } from "./dashboard-receipt";
import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { DashboardReceiptView } from "./dashboard-receipt-view";
import { receiptForTurn } from "./dashboard-receipt";
import { build, result, saved } from "../tests/dashboard-receipts";

function view(receipt:BuildReceipt) {
 const route=createRootRoute({component:()=> <DashboardReceiptView receipt={receipt}/>});
 const dashboard=createRoute({getParentRoute:()=>route,path:'/dashboards/$dashboardId',component:()=>null});
 const router=createRouter({routeTree:route.addChildren([dashboard]),history:createMemoryHistory({initialEntries:['/']})});
 return <MantineProvider><RouterProvider router={router}/></MantineProvider>;
}
it('renders one quiet status, safe chips, exact saved-version link and keyboard details', async () => {
 const host=document.createElement('div');document.body.append(host);const root=createRoot(host);
 try {
  await act(async()=>root.render(view(receiptForTurn(build(),'u')!)));
  expect(host.querySelectorAll('[role="status"]')).toHaveLength(1);
  expect(host.textContent).toContain('Read telemetry · 1 panels · Checked in 1.2 s · Saved v2');
  expect(host.textContent).toContain('~ latency.thresholds');expect(host.textContent).toContain('Layout adjusted');
  expect(host.querySelector('img')).toBeNull();
  const link=host.querySelector('a')!;expect(link.getAttribute('href')).toBe('/dashboards/board');expect(link.textContent).toContain('saved v2');
  const details=host.querySelector<HTMLButtonElement>('button[aria-expanded]')!;expect(details.getAttribute('aria-expanded')).toBe('false');
  details.focus();expect(document.activeElement).toBe(details);
  await act(async()=>details.click());expect(details.getAttribute('aria-expanded')).toBe('true');
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
