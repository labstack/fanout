import { act } from "react";
import { createRoot } from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import { expect, it, vi } from "vitest";
import { pitotTheme, pitotCssVariables } from "../../../../theme";
import { Waterfall } from "./waterfall";
import type { TraceSpan } from "../../../../contracts";

it("marks critical segments with a band and preserves error indicators and accessible descriptions", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node);
  const root = createRoot(node);
  const span: TraceSpan = { span_id: "root", start: new Date(0).toISOString(), duration_ms: 100, operation: "checkout", service: "checkout", kind: "SPAN_KIND_SERVER", status: "STATUS_CODE_ERROR" };
  const render = (showCriticalPath: boolean) => act(async () => root.render(<MantineProvider theme={pitotTheme} cssVariablesResolver={pitotCssVariables}><Waterfall spans={[span]} dark={false} showCriticalPath={showCriticalPath} /></MantineProvider>));
  try {
    await render(true);
    const bar = node.querySelector('[role="img"]')!;
    expect(bar.getAttribute("aria-label")).toContain("on critical path");
    expect(bar.getAttribute("title")).toContain("100.0% of span");
    expect(node.querySelector('[aria-label="Error"]')).not.toBeNull();
    expect(node.querySelectorAll("[data-critical-path-segment]")).toHaveLength(1);
    const band = node.querySelector<HTMLElement>("[data-critical-path-segment]")!;
    expect(band.style.height).toBe("3px");
    expect(band.style.background).toContain("--mantine-color-text");
    expect(band.style.outline).toBe("");
    expect(band.style.minWidth).toBe("");
    await render(false);
    expect(node.querySelectorAll("[data-critical-path-segment]")).toHaveLength(0);
    expect(bar.getAttribute("aria-label")).not.toContain("on critical path");
  } finally {
    await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals();
  }
});

it("aligns fractional bar starts with critical segments", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node);
  const root = createRoot(node);
  const spans: TraceSpan[] = [
    { span_id: "r", start: "2026-11-07T00:00:00.000000100Z", duration_ms: 5, operation: "root", service: "checkout", kind: "SPAN_KIND_SERVER", status: "STATUS_CODE_OK" },
    { span_id: "b", parent_span_id: "r", start: "2026-11-07T00:00:00.003600100Z", duration_ms: 1.3, operation: "child", service: "checkout", kind: "SPAN_KIND_INTERNAL", status: "STATUS_CODE_OK" },
  ];
  try {
    await act(async () => root.render(<MantineProvider theme={pitotTheme} cssVariablesResolver={pitotCssVariables}><Waterfall spans={spans} dark={false} /></MantineProvider>));
    const band = node.querySelector<HTMLElement>('[data-critical-path-segment="b"]')!;
    const fill = band.parentElement!.firstElementChild as HTMLElement;
    expect(parseFloat(fill.style.left)).toBeCloseTo(72, 9);
    expect(band.style.left).toBe(fill.style.left);
    expect(parseFloat(band.style.width)).toBeCloseTo(26, 9);
  } finally {
    await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals();
  }
});
