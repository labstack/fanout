import { expect, it } from "vitest";
import { explainPrompt, fixPrompt } from "./explain";

it("pins the observed window, variables and error without asking to fix", () => {
  const prompt = explainPrompt({ dashboard_id: "board", version: 7, panel_id: "latency", title: "Latency", from_ms: 0, to_ms: 3600000, vars: { service: ["checkout"] }, error: "Invalid measure" });
  expect(prompt).toContain("1970-01-01T00:00:00.000Z");
  expect(prompt).toContain("1970-01-01T01:00:00.000Z");
  expect(prompt).toContain("version 7");
  expect(prompt).toContain("checkout");
  expect(prompt).toContain("Do not create, edit, replace or restore");
  expect(prompt).not.toContain("Please fix");
});

it("includes observed status, diagnosis, staleness and partial notes without claiming nanosecond fidelity", () => {
  const prompt = explainPrompt({ panel_id: "p", title: "P", vars: { service: "$__all" }, from_ms: 0, to_ms: 1, status: "empty", diagnosis: "No rows", stale_since: 42, truncated: true, note: "Partial result" });
  for (const text of ["millisecond-truncated", '"status":"empty"', '"diagnosis":"No rows"', '"stale_since":42', '"truncated":true', '"note":"Partial result"', '"service":"$__all"']) expect(prompt).toContain(text);
});

it("JSON-escapes quotes and newlines in the separate edit prompt", () => {
  const title = 'Latency "checkout"\nnext line', error = 'bad "field"\nquery';
  const prompt = fixPrompt({ dashboard_id: "board", version: 7, panel_id: "p", title, vars: {}, error });
  expect(prompt).toContain(JSON.stringify(title));
  expect(prompt).toContain(JSON.stringify(error));
  expect(prompt).toContain("explicit dashboard edit request");
});

it("explicitly reports a missing executed window and preserves exact authored bounds separately", () => {
  const spec = { time: { from: "2026-10-08T00:00:00.123456789Z", to: "2026-10-08T01:00:00.987654321Z" } };
  const prompt = explainPrompt({ panel_id: "latency", title: "Latency", vars: {}, spec });
  expect(prompt).toContain("No executed window is available; say that explicitly.");
  expect(prompt).toContain(spec.time.from);
  expect(prompt).toContain(spec.time.to);
});
