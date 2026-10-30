import { MantineProvider } from "@mantine/core";
import { fireEvent, within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Query } from "../../../panels/types";
import type { TelemetrySchema } from "../sql-editor/schema";
import { Builder } from "./builder";

const schema: TelemetrySchema = {
  sql_relations: {},
  signals: {
    spans: { columns: [{ name: "service", type: "string" }, { name: "operation", type: "string" }, { name: "duration_ms", type: "number" }, { name: "start_time", type: "time" }, { name: "attributes", type: "map" }], attributes: [{ key: "http.route", scope: "attributes", type: "string" }] },
    logs: { columns: [{ name: "service", type: "string" }, { name: "body", type: "string" }, { name: "severity", type: "string" }] },
  },
  services: [{ value: "checkout" }, { value: "frontend" }],
  measures: ["count", "p95", "avg", "count_distinct", "count_if", "quantile", "share", "last"],
};
const cleanups: (() => void)[] = [];
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }); });
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(query: Query) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const onChange = vi.fn<(next: Query) => void>();
  await act(async () => root.render(<MantineProvider><Builder query={query} schema={schema} onChange={onChange} /></MantineProvider>));
  cleanups.push(() => root.unmount());
  const ui = within(document.body);
  const choose = async (label: string, option: string) => {
    const input = ui.getByLabelText(label, { selector: "input" });
    await act(async () => { fireEvent.focus(input); fireEvent.click(input); fireEvent.keyDown(input, { key: "ArrowDown" }); });
    const list = () => within(document.getElementById(input.getAttribute("aria-controls") ?? "")!);
    await vi.waitFor(() => list().getByRole("option", { name: option, hidden: true }), { timeout: 2000 });
    await act(async () => { fireEvent.click(list().getByRole("option", { name: option, hidden: true })); });
  };
  return { ui, onChange, choose, last: () => onChange.mock.calls.at(-1)![0] };
}

const base: Query = { from: "spans", measures: ["p95(duration_ms)"], where: ["service = 'checkout'"], by: ["operation"], bucket: "5m" };

describe("Builder", () => {
  it("changes a measure function and keeps its field", async () => {
    const { choose, last } = await render(base);
    await choose("Measure 1 function", "avg");
    expect(last()).toEqual({ ...base, measures: ["avg(duration_ms)"] });
  });

  it("drops the field for count and offers only numeric fields to statistics", async () => {
    const { ui, choose, last } = await render(base);
    await choose("Measure 1 function", "count");
    expect(last().measures).toEqual(["count()"]);
    const field = ui.getByLabelText("Measure 1 field", { selector: "input" });
    await act(async () => { fireEvent.focus(field); fireEvent.click(field); fireEvent.keyDown(field, { key: "ArrowDown" }); });
    const list = () => within(document.getElementById(field.getAttribute("aria-controls") ?? "")!);
    await vi.waitFor(() => list().getByRole("option", { name: "duration_ms", hidden: true }));
    expect(list().queryByRole("option", { name: "service", hidden: true })).toBeNull();
  });

  it("edits a filter value with escaping and adds and removes rows", async () => {
    const { ui, onChange, last } = await render({ ...base, where: ["operation = 'GET'"] });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "O'Brien" } }); });
    expect(last().where).toEqual(["operation = 'O''Brien'"]);
    await act(async () => ui.getByRole("button", { name: "Add filter" }).click());
    expect(last().where).toEqual(["operation = 'GET'", "service = ''"]);
    await act(async () => ui.getByRole("button", { name: "Remove filter 1" }).click());
    expect(last().where).toEqual([]);
    expect(onChange).toHaveBeenCalledTimes(3);
  });

  it("writes numeric comparisons unquoted", async () => {
    const { ui, last } = await render({ ...base, where: ["duration_ms > 100"] });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "500" } }); });
    expect(last().where).toEqual(["duration_ms > 500"]);
  });

  it("keeps entries it cannot show as removable chips", async () => {
    const { ui, last } = await render({ ...base, where: ["service IN ('a', 'b')", "service = 'a' OR service = 'b'"], measures: ["count_if(status = 'error') / count()"] });
    expect(ui.getByText("service IN ('a', 'b')")).toBeTruthy();
    expect(ui.getByText("count_if(status = 'error') / count()")).toBeTruthy();
    await act(async () => ui.getByRole("button", { name: "Remove service IN ('a', 'b')" }).click());
    expect(last().where).toEqual(["service = 'a' OR service = 'b'"]);
  });

  it("drops fields the new source lacks and keeps the rest", async () => {
    const { choose, last } = await render({ from: "spans", measures: ["p95(duration_ms)", "count()"], where: ["service = 'checkout'", "duration_ms > 5", "service IN ('a')"], by: ["service", "operation", "attributes['http.route']"] });
    await choose("Source", "logs");
    expect(last()).toEqual({ from: "logs", measures: ["count()"], where: ["service = 'checkout'", "service IN ('a')"], by: ["service", "attributes['http.route']"] });
  });

  it("keeps conditional measures as chips across a source switch", async () => {
    const { ui, choose, last } = await render({ from: "spans", measures: ["count_if(service = 'checkout')", "share()"] });
    expect(ui.getByText("count_if(service = 'checkout')")).toBeTruthy();
    expect(ui.getByLabelText("Measure 2 function", { selector: "input" })).toBeTruthy();
    expect(ui.queryByLabelText("Measure 2 field", { selector: "input" })).toBeNull();
    await choose("Source", "logs");
    expect(last().measures).toEqual(["count_if(service = 'checkout')", "share()"]);
  });

  it("keeps a quoted number quoted and an unquoted number numeric when editing", async () => {
    const { ui, last } = await render({ ...base, where: ["attributes['code'] = 200", "duration_ms > '7'"] });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "404" } }); });
    expect(last().where?.[0]).toBe("attributes['code'] = 404");
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 2 value" }), { target: { value: "9" } }); });
    expect(last().where?.[1]).toBe("duration_ms > '9'");
  });

  it("groups by attribute keys, sets the bucket and the limit", async () => {
    const { ui, choose, last } = await render(base);
    await choose("Add group by field", "attributes['http.route']");
    expect(last().by).toEqual(["operation", "attributes['http.route']"]);
    await choose("Every", "1h");
    expect(last().bucket).toBe("1h");
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Limit" }), { target: { value: "20" } }); });
    expect(last().limit).toBe(20);
  });
});
