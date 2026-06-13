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
    // The app's red token is --danger; there is no raw --destructive var (only
    // the --color-destructive Tailwind alias), so reading --destructive here
    // returned "" and rendered unhealthy nodes/edges black.
    destructive: cssVar("--danger"),
  };
}

// Pass a pre-resolved `chartColors()` when calling per-item (e.g. per graph
// node) to avoid re-reading every CSS var on each call.
export function statusColor(
  status?: string,
  c: ReturnType<typeof chartColors> = chartColors(),
): string {
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

// Inline style for the custom tooltip box used across chart blocks.
// Caller supplies pre-resolved chartColors() to avoid repeated getComputedStyle.
export function tooltipBox(c: ReturnType<typeof chartColors>) {
  return {
    background: c.popover,
    border: `1px solid ${c.border}`,
    color: c.popoverForeground,
    fontSize: 12,
    padding: "6px 8px",
    borderRadius: 4,
  } as const;
}

// Palette for multi-series charts. Cycles via i % length.
export function seriesPalette(): string[] {
  const c = chartColors();
  return [c.primary, c.success, c.warning, c.destructive, cssVar("--accent") || c.primary];
}
