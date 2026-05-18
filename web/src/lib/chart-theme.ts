// Reads canonical CSS token slots at render time (not build time) so charts
// honor light/dark mode and theme switches.

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

export function axisTick(fontSize = 10) {
  return { fill: cssVar("--foreground"), fontSize };
}

export function axisLine() {
  return { stroke: cssVar("--border") };
}

export function gridStroke() {
  return cssVar("--border");
}
