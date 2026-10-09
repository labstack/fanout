import { act, useRef } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it } from "vitest";
import { usePanelViewFocus } from "./use-panel-view-focus";
const cleanups: (()=>void)[]=[];
afterEach(async()=>{await act(async()=>cleanups.splice(0).forEach(fn=>fn()));document.body.innerHTML="";});
async function mount() {
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);cleanups.push(()=>root.unmount());
  let memory!:ReturnType<typeof usePanelViewFocus>;
  function Board({removed=false}:{removed?:boolean}) { const region=useRef<HTMLDivElement>(null);memory=usePanelViewFocus(region);return <div ref={region}>{!removed&&<div data-panel="a"><button data-chart-plot>A</button></div>}<div data-panel="b"><button>B</button></div></div>; }
  const draw=async(removed=false)=>act(async()=>root.render(<Board removed={removed}/>));await draw();
  const a=el.querySelector<HTMLButtonElement>("[data-chart-plot]")!;
  const portal=document.createElement("button");document.body.append(portal);
  return {a,portal,draw,memory:()=>memory};
}
it("consumes restored focus memory before a later portal opens another panel",async()=>{
  const view=await mount();view.a.focus();view.memory().remember("a");
  view.memory().restore()?.focus();expect(document.activeElement).toBe(view.a);
  expect(view.memory().restore()).toBeUndefined();
  view.portal.focus();view.memory().remember("b");expect(view.memory().restore()).toBeUndefined();
});
it("clears an old source when the next view opens from outside the board",async()=>{
  const view=await mount();view.a.focus();view.memory().remember("a");view.portal.focus();view.memory().remember("b");
  expect(view.memory().restore()).toBeUndefined();
});
it("does not restore a panel that has left the board even if its old control is connected",async()=>{
  const view=await mount();view.a.focus();view.memory().remember("a");document.body.append(view.a);
  await view.draw(true);await view.draw();expect(view.memory().restore()).toBeUndefined();
});
