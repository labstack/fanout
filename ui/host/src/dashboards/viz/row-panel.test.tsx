import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Panel, PanelResult } from "../../../../panels/types";
import { bad } from "../../../../tokens";
import { RowPanel } from "./row-panel";
import { TableViz } from "./table";
import { router } from "../../router";
import { makeDrill } from "../drill-state";
import { parseSearch, toSearchParams } from "../search";

// Observe the renderer prop while keeping the real table and cells mounted.
vi.mock("./table", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./table")>();
  return { ...actual, TableViz: vi.fn((props: Parameters<typeof actual.TableViz>[0]) => <actual.TableViz {...props} />) };
});

let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.mocked(TableViz).mockClear();
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

const panel: Panel = { id: "p", title: "Logs", viz: "logs" };
const resultFor = (name: string, text: string): PanelResult => ({
  id: "p", status: "ok", elapsed_ms: 1,
  frame: { columns: [{ name, type: "string", role: "dimension" }], values: [[text]], rows: 1 },
});
const render = async (panel: Panel, result: PanelResult) => {
  await act(async () => root.render(<MantineProvider theme={{ colors: { bad: [...bad] } }}><RowPanel panel={panel} result={result} dark={false} height={200} /></MantineProvider>));
};
const marks = () => [...container.querySelectorAll("tbody mark")].map(mark => mark.textContent);

it.each(["rows", "formatted table"])("builds a %s trace href byte-identical to the router URL", async surface => {
  const previous = window.location.href;
  window.history.replaceState(null, "", "/dashboards/d?range=6h&var-q=%22timeout%22&var-service=%7B%22values%22%3A%5B%5D%7D");
  try {
    const p: Panel = {id: "p", title: "Traces", viz: surface === "rows" ? "traces" : "table", options: surface === "rows" ? undefined : {columns: [{field: "trace_id", format: "trace_link"}]}};
    const result = {...resultFor("trace_id", "0123456789abcdef"), from_ms: 0, to_ms: 60000};
    const target = makeDrill(p, result, {trace_id: "0123456789abcdef", dimensions: {}})!;
    const search = {...parseSearch(router.options.parseSearch!(window.location.search)), drill: JSON.stringify(target)};
    const expected = router.buildLocation({to: "/dashboards/$dashboardId", params: {dashboardId: "d"}, search: toSearchParams(search)});
    if (surface === "rows") await render(p, result);
    else await act(async () => root.render(<MantineProvider><TableViz panel={p} result={result} height={200} /></MantineProvider>));
    const link = container.querySelector<HTMLAnchorElement>("a[href]");
    expect(link).not.toBeNull();
    expect(link!.href).toBe(new URL(expected.href, window.location.origin).href);
  } finally { window.history.replaceState(null, "", previous); }
});

it("clips trace IDs to a fixed width while retaining the full accessible ID", async () => {
  const id = "0123456789abcdef0123456789abcdef";
  await render({...panel,viz:"traces"},resultFor("trace_id",id));
  const link = container.querySelector<HTMLElement>("[data-trace-id]")!;
  expect(link.style.width).toBe("16ch");
  expect(link.style.overflow).toBe("hidden");
  expect(link.style.textOverflow).toBe("ellipsis");
  expect(link.style.whiteSpace).toBe("nowrap");
  expect(link.getAttribute("aria-label")).toContain(id);
  expect(link.title).toBe(id);
});

it.each([["STATUS_CODE_ERROR","Error"],["STATUS_CODE_OK","OK"],["STATUS_CODE_UNSET","Unset"]])("labels OTLP %s as %s", async (value,label) => {
  await render({...panel,viz:"traces"},resultFor("status",value));
  const badge = container.querySelector<HTMLElement>(".mantine-Badge-root")!;
  expect(badge.textContent).toBe(label);
  if (label === "Error") expect(badge.style.getPropertyValue("--badge-bg")).toContain("--mantine-color-bad-");
});

it.each([["a.c", "a.c"], ["(", "("], ["[y", "[y"], ["failed", "FAILED"]])(
  "highlights literal %s case-insensitively without interpreting regex syntax", async (term, expected) => {
    const body = "a.c (x) [y] FAILED abc";
    await render({ ...panel, options: { highlight: term } }, resultFor("body", body));
    expect(marks()).toEqual([expected]);
    expect(container.querySelector("tbody td")?.textContent).toBe(body);
  },
);

it("keeps the cell renderer identity on rerender with identical panel and result", async () => {
  const result = resultFor("body", "failed");
  await render(panel, result);
  const renderer = vi.mocked(TableViz).mock.lastCall![0].renderCell;
  expect(renderer).toBeTypeOf("function");
  await render(panel, result);
  expect(vi.mocked(TableViz).mock.calls).toHaveLength(2);
  expect(vi.mocked(TableViz).mock.lastCall![0].renderCell).toBe(renderer);
});

it("does not fold accents when highlighting log bodies", async () => {
  await render({ ...panel, options: { highlight: "cafe" } }, resultFor("body", "café CAFE"));
  expect(marks()).toEqual(["CAFE"]);
  expect(container.querySelector("tbody td")?.textContent).toBe("café CAFE");
});

it.each([" failed ", " "])("preserves the entire highlight term %j including whitespace", async (term) => {
  await render({ ...panel, options: { highlight: term } }, resultFor("body", "FAILED | FAILED |FAILED"));
  expect(marks()).toEqual(term === " " ? [" ", " ", " "] : [" FAILED "]);
});

it.each(["FATAL", "CRITICAL"])("uses the bad colour for %s and retains its full title with a short severity label", async severity => {
  await render(panel, resultFor("severity", severity));
  const badge = container.querySelector<HTMLElement>(".mantine-Badge-root");
  expect(badge?.textContent).toBe(`◆ ${severity === "CRITICAL" ? "FATAL" : severity}`);
  expect(badge?.title).toBe(severity);
  expect(badge?.style.getPropertyValue("--badge-bg")).toContain("--mantine-color-bad-");
  expect(badge?.hidden).toBe(false);
});

it("highlights log pattern body templates while preserving monospace text", async () => {
  await render({ ...panel, viz: "log_patterns", options: { highlight: "failed" } }, resultFor("body_template", "FAILED <*> café"));
  expect(marks()).toEqual(["FAILED"]);
  expect(container.querySelector("tbody td")?.textContent).toBe("FAILED <*> café");
  expect(container.querySelector<HTMLElement>("[data-row-text] span")?.style.fontFamily).toContain("monospace");
  expect(container.querySelector<HTMLElement>("[data-row-text] span")?.style.fontSize).toBe("12px");
  await render({ ...panel, viz: "log_patterns", options: { highlight: "cafe" } }, resultFor("body_template", "FAILED <*> café"));
  expect(marks()).toEqual([]);
});
