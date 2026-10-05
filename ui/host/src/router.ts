import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

/** Keep primitives readable; only object values need JSON serialization. */
function stringifySearch(search: Record<string, unknown>): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(search)) {
    for (const item of Array.isArray(value) ? value : [value]) {
      if (item === undefined) continue;
      params.append(key, item !== null && typeof item === "object" ? JSON.stringify(item) : String(item));
    }
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

function parseSearch(query: string): Record<string, unknown> {
  const search: Record<string, unknown> = Object.create(null);
  const decode = (value: string): unknown => {
    if (value.startsWith("{")) {
      try {
        const object: unknown = JSON.parse(value);
        if (object !== null && typeof object === "object" && !Array.isArray(object)) return object;
      } catch { /* A literal value that resembles JSON remains a string. */ }
    }
    return value;
  };
  const params = new URLSearchParams(query);
  for (const key of new Set(params.keys())) {
    const values = params.getAll(key).map(decode);
    search[key] = values.length === 1 ? values[0] : values;
  }
  return search;
}

export const router = createRouter({
  routeTree,
  parseSearch,
  stringifySearch,
  defaultPreload: "intent",
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
