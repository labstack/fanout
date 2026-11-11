import { expect, it, vi } from "vitest";
import * as fragments from "../../panels/fragment";
import { mcpAppContent } from "./mcp-app-content";
import { fixture } from "../tests/fixtures";
it("validates each immutable activity once across chat and frame boundaries", () => {
  const decode = vi.spyOn(fragments, "panelFragment");
  const raw = { resource_uri: "ui://pitot/panels.html", tool_name: "query_telemetry", tool_input: {}, tool_result: fixture(), is_error: false };
  const content = mcpAppContent(raw);
  expect(content).toBeDefined(); expect(mcpAppContent(content)).toBe(content);
  expect(decode).toHaveBeenCalledTimes(1);
  expect(mcpAppContent({ ...raw, tool_result: "old persisted result" })).toBeUndefined();
  decode.mockRestore();
});

it("renders saved pre-rename activities without rewriting their stored URI", () => {
  const raw = { resource_uri: "ui://fanout/panels.html", tool_name: "query_telemetry", tool_input: {}, tool_result: fixture(), is_error: false };
  const content = mcpAppContent(raw);
  expect(content?.resource_uri).toBe("ui://pitot/panels.html");
  expect(raw.resource_uri).toBe("ui://fanout/panels.html");
  expect(mcpAppContent({ ...raw, tool_result: "invalid" })).toBeUndefined();
});
