import { act } from "react";
import { createRoot } from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import { expect, it, vi } from "vitest";
import { fanoutTheme, fanoutCssVariables } from "../../../../theme";
import { Waterfall } from "./waterfall";
import type { TraceSpan } from "../../../../contracts";

it("outlines critical segments without replacing error fills or losing accessible descriptions", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node);
  const root = createRoot(node);
  const span: TraceSpan = { span_id: "root", start: new Date(0).toISOString(), duration_ms: 100, operation: "checkout", service: "checkout", kind: "server", status: "STATUS_CODE_ERROR" };
  const render = (showCriticalPath: boolean) => act(async () => root.render(<MantineProvider theme={fanoutTheme} cssVariablesResolver={fanoutCssVariables}><Waterfall spans={[span]} dark={false} showCriticalPath={showCriticalPath} /></MantineProvider>));
  try {
    await render(true);
    const bar = node.querySelector('[role="img"]')!;
    expect(bar.getAttribute("aria-label")).toContain("on critical path");
    expect(bar.getAttribute("title")).toContain("100.0% of span");
    const fill = bar.firstElementChild as HTMLElement;
    expect(fill.style.background).toContain("bad");
    expect(node.querySelector('[aria-label="Error"]')).not.toBeNull();
    expect(node.querySelectorAll("[data-critical-path-segment]")).toHaveLength(1);
    await render(false);
    expect(node.querySelectorAll("[data-critical-path-segment]")).toHaveLength(0);
    expect(fill.style.background).toContain("bad");
    expect(bar.getAttribute("aria-label")).toContain("on critical path");
  } finally {
    await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals();
  }
});
