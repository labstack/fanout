/**
 * Shared chart theming for Recharts + D3. Reads from canonical CSS token slots
 * defined in index.css so charts honor light/dark mode and theme switches.
 */

export function cssVar(name: string): string {
  return getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();
}

export function chartColors() {
  return {
    foreground: cssVar("--foreground"),
    mutedForeground: cssVar("--muted-foreground"),
    border: cssVar("--border"),
    popover: cssVar("--popover"),
    popoverForeground: cssVar("--popover-foreground"),
    primary: cssVar("--primary"),
    success: cssVar("--success"),
    warning: cssVar("--warning"),
    destructive: cssVar("--destructive"),
  };
}

export function statusColor(status?: string): string {
  const c = chartColors();
  switch (status?.toLowerCase()) {
    case "healthy":
      return c.success;
    case "degraded":
      return c.warning;
    case "unhealthy":
      return c.destructive;
    default:
      return c.mutedForeground;
  }
}

/** Standard axis tick style for Recharts <XAxis> / <YAxis tick={...}>. */
export function axisTick(fontSize = 10) {
  return { fill: cssVar("--foreground"), fontSize };
}

/** Standard axis line style. */
export function axisLine() {
  return { stroke: cssVar("--border") };
}

/** Dashed grid line style for <CartesianGrid>. */
export function gridStroke() {
  return cssVar("--border");
}
