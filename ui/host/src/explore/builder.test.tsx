import { MantineProvider } from "@mantine/core";
import { fireEvent, within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Query } from "../../../panels/types";
import type { TelemetrySchema } from "../sql-editor/schema";
import { Builder, querySchema } from "./builder";

const schema: TelemetrySchema = {
  sql_relations: {},
  signals: {
    spans: { columns: [{ name: "service", type: "string" }, { name: "operation", type: "string" }, { name: "duration_ms", type: "number" }, { name: "start_time", type: "time" }, { name: "attributes", type: "map" }], attributes: [{ key: "http.route", scope: "attributes", type: "string" }] },
    logs: { columns: [{ name: "service", type: "string" }, { name: "body", type: "string" }, { name: "severity", type: "string" }] },
  },
  services: [{ value: "checkout" }, { value: "frontend" }],
  measures: ["count", "p95", "avg", "count_distinct", "count_if", "quantile", "share", "last"],
  buckets: ["auto", "10s", "30s", "1m", "5m", "10m", "15m", "30m", "1h", "3h", "6h", "12h", "1d"],
};
const cleanups: (() => void)[] = [];
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }); });
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(query: Query, labels: Record<string, string> = {}) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  // The page feeds each change back, as the Builder's owner does.
  const draw = (current: Query) => root.render(<MantineProvider><Builder query={current} schema={schema} labels={labels} onChange={onChange} onInvalid={onInvalid} /></MantineProvider>);
  const onChange = vi.fn<(next: Query) => void>(next => draw(next));
  const onInvalid = vi.fn<(invalid: boolean) => void>();
  await act(async () => draw(query));
  cleanups.push(() => root.unmount());
  const ui = within(document.body);
  const choose = async (label: string, option: string) => {
    const input = ui.getByLabelText(label, { selector: "input" });
    await act(async () => { fireEvent.focus(input); fireEvent.click(input); fireEvent.keyDown(input, { key: "ArrowDown" }); });
    const list = () => within(document.getElementById(input.getAttribute("aria-controls") ?? "")!);
    await vi.waitFor(() => list().getByRole("option", { name: option, hidden: true }), { timeout: 2000 });
    await act(async () => { fireEvent.click(list().getByRole("option", { name: option, hidden: true })); });
  };
  return { ui, onChange, onInvalid, choose, last: () => onChange.mock.calls.at(-1)![0] };
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
    const field = ui.getByLabelText("Measure 1 field", { selector: "input" });
    await act(async () => { fireEvent.focus(field); fireEvent.click(field); fireEvent.keyDown(field, { key: "ArrowDown" }); });
    const list = () => within(document.getElementById(field.getAttribute("aria-controls") ?? "")!);
    await vi.waitFor(() => list().getByRole("option", { name: "duration_ms", hidden: true }));
    expect(list().queryByRole("option", { name: "service", hidden: true })).toBeNull();
    await act(async () => { fireEvent.keyDown(field, { key: "Escape" }); });
    await choose("Measure 1 function", "count");
    expect(last().measures).toEqual(["count()"]);
    expect(ui.queryByLabelText("Measure 1 field", { selector: "input" })).toBeNull();
  });

  it("edits a filter value with escaping and adds and removes rows", async () => {
    const { ui, onChange, last } = await render({ ...base, where: ["operation = 'GET'"] });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "O'Brien" } }); });
    expect(last().where).toEqual(["operation = 'O''Brien'"]);
    await act(async () => ui.getByRole("button", { name: "Remove filter 1" }).click());
    expect(last().where).toEqual([]);
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it("adds a filter to the query only once it has a value, and numeric fields compare numbers", async () => {
    const { ui, onChange, choose, last } = await render({ ...base, where: [] });
    await act(async () => ui.getByRole("button", { name: "Add filter" }).click());
    expect(onChange).not.toHaveBeenCalled();
    await choose("Filter 1 field", "duration_ms");
    await choose("Filter 1 operator", ">");
    expect(onChange).not.toHaveBeenCalled();
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "100" } }); });
    expect(last().where).toEqual(["duration_ms > 100"]);
  });

  it("keeps a cleared filter in its place but out of the query", async () => {
    const { ui, last } = await render({ ...base, where: ["operation = 'GET'", "service = 'checkout'"] });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "" } }); });
    expect(last().where).toEqual(["service = 'checkout'"]);
    expect(ui.getByLabelText("Filter 1 field", { selector: "input" })).toHaveProperty("value", "operation");
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "POST" } }); });
    expect(last().where).toEqual(["operation = 'POST'", "service = 'checkout'"]);
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

  it("carries over what the new source has, counts rows when no measure fits, and says what it removed", async () => {
    const { ui, choose, last } = await render({ from: "spans", measures: ["p95(duration_ms)"], where: ["kind = 'server'"], bucket: "5m" });
    await choose("Source", "logs");
    expect(last()).toEqual({ from: "logs", measures: ["count()"], where: [], by: [], bucket: "5m" });
    expect(ui.getByRole("status").textContent).toBe("Removed what logs does not have: p95(duration_ms), kind = 'server'.");
  });

  it("lists every bucket, including short ones", async () => {
    const { ui } = await render({ ...base, bucket: "10s" });
    expect(ui.getByLabelText("Every", { selector: "input" })).toHaveProperty("value", "10s");
  });

  it("keeps conditional measures as chips across a source switch", async () => {
    const { ui, choose, last } = await render({ from: "spans", measures: ["count_if(service = 'checkout')", "share()"] });
    expect(ui.getByText("count_if(service = 'checkout')")).toBeTruthy();
    expect(ui.getByLabelText("Measure 2 function", { selector: "input" })).toBeTruthy();
    expect(ui.queryByLabelText("Measure 2 field", { selector: "input" })).toBeNull();
    await choose("Source", "logs");
    expect(last().measures).toEqual(["count_if(service = 'checkout')", "share()"]);
  });

  it("keeps how an attribute value was typed and compares schema numbers as numbers", async () => {
    const { ui, last } = await render({ ...base, where: ["attributes['code'] = 200", "duration_ms > '7'"] });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "404" } }); });
    expect(last().where?.[0]).toBe("attributes['code'] = 404");
    // A schema field's type decides: a number compares as a number.
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 2 value" }), { target: { value: "9" } }); });
    expect(last().where?.[1]).toBe("duration_ms > 9");
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

  it("takes durations with units for duration_ms and shows them in their unit", async () => {
    const { ui, last } = await render({ ...base, where: ["duration_ms > 1500"] });
    const value = () => ui.getByRole<HTMLInputElement>("textbox", { name: "Filter 1 value" });
    expect(value().value).toBe("1.5s");
    await act(async () => { fireEvent.change(value(), { target: { value: "100ms" } }); });
    expect(last().where).toEqual(["duration_ms > 100"]);
    expect(value().value).toBe("100ms");
    await act(async () => { fireEvent.change(value(), { target: { value: "2s" } }); fireEvent.blur(value()); });
    expect(last().where).toEqual(["duration_ms > 2000"]);
    expect(value().value).toBe("2s");
  });

  it("marks a value a numeric field cannot use and keeps it out of the query", async () => {
    const { ui, last, onInvalid } = await render({ ...base, where: ["duration_ms > 100", "service = 'checkout'"] });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "fast" } }); });
    expect(last().where).toEqual(["service = 'checkout'"]);
    expect(ui.getByText("Enter a duration such as 100ms or 1.5s")).toBeTruthy();
    expect(onInvalid).toHaveBeenLastCalledWith(true);
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "250ms" } }); });
    expect(last().where).toEqual(["duration_ms > 250", "service = 'checkout'"]);
    expect(ui.queryByText("Enter a duration such as 100ms or 1.5s")).toBeNull();
    expect(onInvalid).toHaveBeenLastCalledWith(false);
  });

  it("shows entries it cannot edit as FanQL, also when a source switch removes them", async () => {
    const labels = { "contains(lower(operation), lower('se'))": "se", "status = 'STATUS_CODE_ERROR'": "status:error" };
    const { ui, choose } = await render({ from: "spans", measures: ["count()"], where: ["contains(lower(operation), lower('se'))", "status = 'STATUS_CODE_ERROR'"] }, labels);
    expect(ui.getByText("se")).toBeTruthy();
    expect(ui.queryByText(/contains\(lower/)).toBeNull();
    await choose("Source", "logs");
    expect(ui.getByRole("status").textContent).toBe("Removed what logs does not have: status:error.");
  });

  it("offers the query's own fields and measures when the schema is unavailable", () => {
    expect(querySchema({ from: "spans", measures: ["p95(duration_ms)", "count_distinct(trace_id)"], where: ["operation = 'GET'", "duration_ms > 5"], by: ["operation"], bucket: "5m" })).toEqual({
      signals: { spans: { columns: [{ name: "duration_ms", type: "number" }, { name: "trace_id", type: "string" }, { name: "operation", type: "string" }, { name: "service", type: "string" }] } },
      sql_relations: {}, measures: ["count", "p95", "count_distinct"], buckets: ["auto", "5m"],
    });
  });
});
