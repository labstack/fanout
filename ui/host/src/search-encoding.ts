/** Keep primitives readable; serialize objects and quote strings beginning with { or ". */
export function stringifySearch(search: Record<string, unknown>): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(search)) {
    for (const item of Array.isArray(value) ? value : [value]) {
      if (item === undefined) continue;
      // Quote strings that could otherwise be decoded as structured objects.
      const structuredString = typeof item === "string" && /^[{"]/.test(item);
      params.append(key, structuredString || item !== null && typeof item === "object" ? JSON.stringify(item) : String(item));
    }
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

export function parseSearch(query: string): Record<string, unknown> {
  const search: Record<string, unknown> = Object.create(null);
  const decode = (value: string): unknown => {
    if (value.startsWith("{") || value.startsWith('"')) {
      try {
        const object: unknown = JSON.parse(value);
        if (typeof object === "string") return /^[{"]/.test(object) ? object : value;
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

