type ResourceCSP = {
  connectDomains?: unknown;
  resourceDomains?: unknown;
  frameDomains?: unknown;
  baseUriDomains?: unknown;
};

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function cspSources(value: unknown, schemes: string[]): string[] {
  if (!Array.isArray(value)) return [];
  const allowed = new Set(schemes);
  return value.filter((source): source is string => {
    if (typeof source !== "string" || /[\s;'\"]/.test(source)) return false;
    const match = source.match(/^([a-z]+):\/\/([^/]+)$/i);
    return Boolean(match && allowed.has(match[1].toLowerCase()));
  });
}

export function mcpAppCSP(meta: unknown): string {
  const csp = record(record(record(meta)?.ui)?.csp) as ResourceCSP | undefined;
  const connect = cspSources(csp?.connectDomains, ["http", "https", "ws", "wss"]);
  const resources = cspSources(csp?.resourceDomains, ["http", "https"]);
  const frames = cspSources(csp?.frameDomains, ["http", "https"]);
  const bases = cspSources(csp?.baseUriDomains, ["http", "https"]);
  const resourceSuffix = resources.length ? ` ${resources.join(" ")}` : "";
  const directives = [
    "default-src 'none'",
    "worker-src blob:",
    `script-src 'self' 'unsafe-inline'${resourceSuffix}`,
    `style-src 'self' 'unsafe-inline'${resourceSuffix}`,
    `img-src 'self' data:${resourceSuffix}`,
    `media-src 'self' data:${resourceSuffix}`,
    // The apps build inlines their woff2 files as data: URIs; without this
    // every embedded view falls back to the system font.
    `font-src 'self' data:${resourceSuffix}`,
    `connect-src ${connect.length ? connect.join(" ") : "'none'"}`,
  ];
  if (frames.length) directives.push(`frame-src ${frames.join(" ")}`);
  if (bases.length) directives.push(`base-uri ${bases.join(" ")}`);
  return `${directives.join("; ")};`;
}

