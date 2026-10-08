import type { App } from "@modelcontextprotocol/ext-apps";
import type { PanelFragment } from "../../../panels/fragment";
import { panelFragment } from "../../../panels/fragment";
import type { QueryBody } from "../dashboards/api";
import type { DrillClient } from "../dashboards/drill-client";

export function appTransport(app: App) {
  async function call<T>(name: string, args: Record<string, unknown>, signal?: AbortSignal): Promise<T> {
    if (signal?.aborted) throw new DOMException("Canceled", "AbortError");
    const result = await app.callServerTool({ name, arguments: args }, { signal });
    if (signal?.aborted) throw new DOMException("Canceled", "AbortError");
    if (result.isError || !result.structuredContent) throw new Error("This view could not be refreshed.");
    return result.structuredContent as T;
  }
  const drill: DrillClient = {
    exemplars: (body, signal) => call("get_panel_exemplars", { ...body }, signal),
    trace: async (target, signal) => {
      const fragment = panelFragment(await call<PanelFragment>("inspect_trace", { trace_id: target.trace_id, namespace: target.namespace, from: target.window_from, to: target.window_to, limit: 200 }, signal));
      if (!fragment.trace) throw new Error("Trace unavailable");
      return fragment.trace;
    },
  };
  return {
    drill,
    query: async (body: Omit<QueryBody, "panels">) => {
      if (Object.hasOwn(body, "panels")) throw new Error("Panel subsets are unavailable in chat fragments");
      const raw = await call<PanelFragment>("query_panel_fragment", { ...body });
      const next = panelFragment(raw);
      next.vars = body.vars; return next;
    },
    resolveVariables: async (body: Omit<QueryBody, "panels" | "widths" | "compare">, signal?: AbortSignal) => {
      const result = await call<{ options: Record<string, { value: string; count?: number }[]> }>("resolve_panel_variables", { ...body }, signal);
      if (!result.options || typeof result.options !== "object" || Array.isArray(result.options) || Object.values(result.options).some(options => !Array.isArray(options) || options.some(o => !o || typeof o.value !== "string"))) throw new Error("Invalid variable options");
      return result.options;
    },
  };
}
