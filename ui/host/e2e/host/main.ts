import { AppBridge, PostMessageTransport } from "@modelcontextprotocol/ext-apps/app-bridge";
import recordings from "../data/mcp.json" with { type: "json" };

type ToolResult = Parameters<AppBridge["sendToolResult"]>[0];
type Recording = { input: { name: string; arguments: Record<string, unknown> }; result: ToolResult; helpers: { input: { name: string; arguments: Record<string, unknown> }; result: ToolResult }[] };
const data = recordings as unknown as Record<string, Recording>;
const params = new URLSearchParams(location.search);
const scenario = params.get("scenario") ?? "overview";
const theme = params.get("theme") === "dark" ? "dark" : "light";
const iframe = document.querySelector("iframe")!;
const fail = (message: string): never => { queueMicrotask(() => { throw new Error(message); }); throw new Error(message); };
const html = await fetch("/panels.html").then(response => response.text());
const loaded = new Promise<void>(resolve => iframe.addEventListener("load", () => resolve(), { once: true }));
iframe.srcdoc = html;
await loaded;
if (scenario === "unavailable") document.body.dataset.ready = "true";
else {
  const recording = data[scenario === "cancelled" ? "overview" : scenario] ?? fail(`Unknown scenario: ${scenario}`);
  const bridge = new AppBridge(null, { name: "Fanout test host", version: "1.0.0" }, { serverTools: {}, logging: {}, serverResources: {} }, {
    hostContext: { theme, displayMode: "inline", availableDisplayModes: ["inline"] },
  });
  const canonical = (value: unknown): string => JSON.stringify(value, (_key, item) => item && typeof item === "object" && !Array.isArray(item) ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b))) : item);
  bridge.oncalltool = async input => {
    const request = { name: input.name, arguments: input.arguments ?? {} };
    const match = recording.helpers.find(helper => canonical(helper.input) === canonical(request));
    return match?.result ?? fail(`Unexpected tools/call: ${canonical(input)}`);
  };
  bridge.onreadresource = async ({ uri }) => uri === "ui://fanout/panels.html"
    ? { contents: [{ uri, mimeType: "text/html;profile=mcp-app", text: html }] } : fail(`Unexpected resources/read: ${uri}`);
  bridge.onopenlink = async ({ url }) => fail(`Unexpected ui/open-link: ${url}`);
  bridge.onrequestdisplaymode = async ({ mode }) => mode === "inline" ? { mode } : fail(`Unexpected display mode: ${mode}`);
  bridge.onsizechange = ({ height }) => { if (height) iframe.style.height = `${Math.min(2000, Math.max(240, Math.round(height)))}px`; };
  // Requests outside the recorded protocol must fail the test, including methods
  // the SDK would otherwise answer with a method-not-found result.
  window.addEventListener("message", event => {
    if (event.source !== iframe.contentWindow || event.data?.id === undefined || !event.data?.method) return;
    if (!["ui/initialize", "tools/call", "resources/read", "ui/open-link", "ui/request-display-mode"].includes(event.data.method)) fail(`Unexpected request: ${event.data.method}`);
  });
  async function deliver() {
    if (scenario === "cancelled") await bridge.sendToolCancelled({ reason: "Synthetic host cancellation" });
    else {
      const structuredContent = recording.result.structuredContent ?? JSON.parse((recording.result.content?.find(item => item.type === "text") as { text: string }).text);
      await bridge.sendToolResult({ ...recording.result, structuredContent, content: [{ type: "text", text: JSON.stringify(structuredContent) }] });
    }
  }
  Object.assign(window, { deliver });
  bridge.oninitialized = async () => {
    await bridge.sendToolInput({ arguments: recording.input.arguments });
    if (!params.has("hold")) await deliver();
    document.body.dataset.ready = "true";
  };
  await bridge.connect(new PostMessageTransport(iframe.contentWindow!, iframe.contentWindow!));
}
