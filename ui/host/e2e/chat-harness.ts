import type { Page } from "@playwright/test";

export async function mountApp(page: Page, html: string, fragment: unknown, theme: "light" | "dark") {
  // A same-origin harness document owns the host-side transport; the iframe has an opaque origin.
  await page.route("http://fanout-harness.test/**", route => route.fulfill({ contentType: "text/html", body: "<!doctype html><html><body style='margin:0'><iframe title='Fragment smoke' sandbox='allow-scripts' style='display:block;width:100%;height:240px;border:0'></iframe></body></html>" }));
  await page.goto("http://fanout-harness.test/");
  await page.evaluate(({ html, fragment, theme }) => {
    const iframe = document.querySelector("iframe")!;
    const host = window as typeof window & { smokeSizes: number[]; smokeDelivered: boolean };
    host.smokeSizes = []; host.smokeDelivered = false;
    const send = (message: unknown) => iframe.contentWindow!.postMessage(message, "*");
    window.addEventListener("message", event => {
      if (event.source !== iframe.contentWindow || !event.data || event.data.jsonrpc !== "2.0") return;
      const message = event.data;
      if (message.method === "ui/initialize") {
        send({ jsonrpc: "2.0", id: message.id, result: { protocolVersion: message.params.protocolVersion,
          hostInfo: { name: "Fanout smoke", version: "1.0.0" }, hostCapabilities: { serverTools: {}, logging: {} },
          hostContext: { theme, displayMode: "inline", availableDisplayModes: ["inline", "fullscreen"] } } });
      } else if (message.method === "ui/notifications/initialized") {
        send({ jsonrpc: "2.0", method: "ui/notifications/tool-input", params: { arguments: {} } });
        send({ jsonrpc: "2.0", method: "ui/notifications/tool-result", params: { content: [{ type: "text", text: JSON.stringify(fragment) }], structuredContent: fragment, isError: false } });
        host.smokeDelivered = true;
      } else if (message.method === "ui/notifications/size-changed") {
        host.smokeSizes.push(performance.now());
        // Mirror maxAppHeight in ui/host/src/mcp-app-frame.tsx (not exported).
        if (message.params.height) iframe.style.height = `${Math.min(2000, Math.max(240, Math.round(message.params.height)))}px`;
      } else if (message.id !== undefined) {
        send({ jsonrpc: "2.0", id: message.id, error: { code: -32601, message: "Smoke harness has no interactive tools" } });
      }
    });
    iframe.srcdoc = html;
  }, { html, fragment, theme });
}

