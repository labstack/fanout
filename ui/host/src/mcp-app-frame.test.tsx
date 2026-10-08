import { MantineProvider } from "@mantine/core";
import { act, type ComponentType } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
const uri="ui://fanout/panels.html";
const fragment={dashboard:{version:1,name:"Fixture",panels:[{id:"text",title:"Text",viz:"text",content:"hello"}]},results:[{id:"text",status:"ok"}]};
const content={resource_uri:uri,tool_name:"query_telemetry",tool_input:{},tool_result:fragment,is_error:false};
const mcp=vi.hoisted(()=>({connect:vi.fn(),readResource:vi.fn(),listTools:vi.fn(),callTool:vi.fn(),close:vi.fn(),clients:[] as Array<{onclose?:()=>void}>,bridges:[] as Array<{oncalltool?:(params:{name:string},extra:{mcpReq:{signal:AbortSignal}})=>Promise<unknown>;onsizechange?:(size:{height:number})=>void}>,capabilities:[] as unknown[]}));
vi.mock("@modelcontextprotocol/client",()=>({Client:class {onclose?:()=>void;onerror?:()=>void;constructor(){mcp.clients.push(this);}connect=mcp.connect;readResource=mcp.readResource;listTools=mcp.listTools;callTool=mcp.callTool;close=mcp.close;},StreamableHTTPClientTransport:class {close=mcp.close;}}));
vi.mock("@modelcontextprotocol/ext-apps/app-bridge",()=>({AppBridge:class {oninitialized?:()=>Promise<void>;oncalltool?:()=>Promise<unknown>;onsizechange?:()=>void;constructor(client:unknown,_info:unknown,capabilities:unknown){expect(client).toBeNull();mcp.bridges.push(this);mcp.capabilities.push(capabilities);}async connect(){await this.oninitialized?.();}async sendToolInput(){}async sendToolResult(){}async teardownResource(){}setHostContext(){}},PostMessageTransport:class {}}));
let MCPAppFrame:ComponentType<{content:unknown}>;
let mcpAppCSP:(meta:unknown)=>string;
beforeEach(async()=>{await new Promise(r=>setTimeout(r,0));vi.resetModules();vi.clearAllMocks();mcp.clients.length=0;mcp.bridges.length=0;mcp.capabilities.length=0;
 mcp.connect.mockResolvedValue(undefined);mcp.close.mockResolvedValue(undefined);mcp.callTool.mockResolvedValue({content:[]});
 mcp.readResource.mockImplementation(({uri})=>Promise.resolve({contents:[{uri,mimeType:"text/html;profile=mcp-app",text:"<!doctype html><html><head></head><body>app</body></html>",_meta:{ui:{csp:{}}}}],ttlMs:300000}));
 mcp.listTools.mockResolvedValue({tools:[{name:"query_panel_fragment",_meta:{ui:{visibility:["app"]}}},{name:"inspect_trace",_meta:{ui:{visibility:["model","app"]}}},{name:"create_dashboard"},{name:"replace_dashboard"},{name:"model_tool",_meta:{ui:{visibility:["model"]}}}]});
 ({default:MCPAppFrame,mcpAppCSP}=await import("./mcp-app-frame"));
});
async function mount(contents:unknown[]=[content]){const container=document.createElement("div");document.body.append(container);const root=createRoot(container);await act(async()=>root.render(<MantineProvider>{contents.map((value,i)=><MCPAppFrame key={i} content={value}/>)}</MantineProvider>));return {container,root,async unmount(){await act(async()=>root.unmount());container.remove();}};}
describe("MCPAppFrame",()=>{
 it("retries a mounted view through a server restart with failed connect and backoff",async()=>{
  vi.useFakeTimers();const view=await mount();
  try {
   expect(view.container.querySelector("iframe")).not.toBeNull();
   mcp.connect.mockRejectedValueOnce(new Error("server restarting"));
   await act(async()=>{mcp.clients[0].onclose?.();await vi.advanceTimersByTimeAsync(0);});
   expect(mcp.connect).toHaveBeenCalledTimes(2);
   await act(async()=>vi.advanceTimersByTimeAsync(749));expect(mcp.connect).toHaveBeenCalledTimes(2);
   await act(async()=>vi.advanceTimersByTimeAsync(1));expect(mcp.connect).toHaveBeenCalledTimes(3);
   expect(view.container.querySelector("iframe")).not.toBeNull();expect(mcp.readResource).toHaveBeenCalledTimes(1);
  } finally {await view.unmount();vi.useRealTimers();}
 });
 it("does not retry a permanently invalid resource after reconnecting past cache expiry",async()=>{
  vi.useFakeTimers();const view=await mount();
  try {
   expect(view.container.querySelector("iframe")).not.toBeNull();
   await act(async()=>vi.advanceTimersByTimeAsync(300001));
   mcp.readResource.mockResolvedValueOnce({contents:[{uri,mimeType:"text/html",text:"wrong profile"}]});
   await act(async()=>{mcp.clients[0].onclose?.();await vi.advanceTimersByTimeAsync(0);});
   expect(view.container.textContent).toContain("This view could not be loaded");
   await act(async()=>vi.advanceTimersByTimeAsync(60000));
   expect(mcp.connect).toHaveBeenCalledTimes(2);expect(mcp.readResource).toHaveBeenCalledTimes(2);
  } finally {await view.unmount();vi.useRealTimers();}
 });
 it("rejects iframe chat messages and uses the exact intrinsic integer height", async()=>{
  const view=await mount();await vi.waitFor(()=>expect(view.container.querySelector("iframe")).not.toBeNull());
  await act(async()=>view.container.querySelector("iframe")!.dispatchEvent(new Event("load")));
  const bridge=mcp.bridges[0] as typeof mcp.bridges[0] & {onmessage(params:{content:unknown[]}):Promise<{isError?:boolean}>};
  await expect(bridge.onmessage({content:[{type:"text",text:"create a dashboard"}]})).resolves.toEqual({isError:true});
  expect(view.container.querySelector("iframe")?.style.height).toBe("240px");
  await act(async()=>bridge.onsizechange?.({height:470.03}));
  expect(view.container.querySelector("iframe")?.style.height).toBe("470px");
  await view.unmount();
 });
 it("shares resource and negotiated catalog across concurrent activities and reconnects",async()=>{const view=await mount([content,{...content,tool_name:"search_logs"}]);await vi.waitFor(()=>expect(view.container.querySelectorAll("iframe")).toHaveLength(2));expect(mcp.connect).toHaveBeenCalledTimes(1);expect(mcp.readResource).toHaveBeenCalledTimes(1);expect(mcp.listTools).toHaveBeenCalledTimes(1);for(const frame of view.container.querySelectorAll("iframe")){expect(frame.getAttribute("sandbox")).toBe("allow-scripts");expect(frame.getAttribute("srcdoc")).toContain("connect-src 'none'");await act(async()=>frame.dispatchEvent(new Event("load")));}expect(mcp.capabilities).toEqual([{serverTools:{},logging:{}},{serverTools:{},logging:{}}]);await act(async()=>mcp.bridges[0].onsizechange?.({height:5000}));expect(view.container.querySelector("iframe")?.style.height).toBe("2000px");await act(async()=>mcp.clients[0].onclose?.());await vi.waitFor(()=>expect(mcp.connect).toHaveBeenCalledTimes(2));expect(mcp.readResource).toHaveBeenCalledTimes(1);await view.unmount();});
 it("rejects forged mutations, unknown and model-only tools before forwarding; preserves cancellation",async()=>{const view=await mount();await vi.waitFor(()=>expect(view.container.querySelector("iframe")).not.toBeNull());await act(async()=>view.container.querySelector("iframe")!.dispatchEvent(new Event("load")));const controller=new AbortController();const extra={mcpReq:{signal:controller.signal}};for(const name of ["create_dashboard","edit_dashboard","replace_dashboard","restore_dashboard","unknown","model_tool"]){await expect(mcp.bridges[0].oncalltool!({name},extra)).rejects.toThrow(/unavailable/);}expect(mcp.callTool).not.toHaveBeenCalled();await mcp.bridges[0].oncalltool!({name:"query_panel_fragment"},extra);expect(mcp.callTool).toHaveBeenCalledWith({name:"query_panel_fragment"},{signal:controller.signal});await view.unmount();});
 it.each([null,{}, {resourceUri:uri,toolName:"query_telemetry"},{...content,resource_uri:"ui://fanout/log-explorer.html"},{...content,tool_result:{data:{}}},{...content,tool_input:undefined}])("rejects invalid persisted activity without connecting (%s)",async value=>{const view=await mount([value]);expect(view.container.textContent).toContain("This view could not be loaded. Please try again.");expect(mcp.connect).not.toHaveBeenCalled();expect(mcp.readResource).not.toHaveBeenCalled();await view.unmount();});
 it.each(["uri","mime","empty","failure"])("evicts failed %s resource reads",async mode=>{if(mode==="failure")mcp.readResource.mockRejectedValueOnce(new Error("offline"));else mcp.readResource.mockResolvedValueOnce({contents:[{uri:mode==="uri"?"wrong":uri,mimeType:mode==="mime"?"text/html":"text/html;profile=mcp-app",text:mode==="empty"?"":"<html></html>"}]});let view=await mount();await vi.waitFor(()=>expect(view.container.textContent).toContain("This view could not be loaded"));await view.unmount();view=await mount();await vi.waitFor(()=>expect(view.container.querySelector("iframe")).not.toBeNull());expect(mcp.readResource).toHaveBeenCalledTimes(2);await view.unmount();});
 it("builds CSP only from safe declared sources and permits inline fonts and Blob workers",()=>{const policy=mcpAppCSP({ui:{csp:{connectDomains:["https://api.example.com","https://evil;script-src"],resourceDomains:["https://cdn.example.com"],frameDomains:["https://frames.example.com"]}}});expect(policy).toContain("connect-src https://api.example.com");expect(policy).toContain("font-src 'self' data: https://cdn.example.com");expect(policy).not.toContain("evil");expect(policy).toContain("worker-src blob:");expect(mcpAppCSP(undefined)).toContain("connect-src 'none'");});
});
