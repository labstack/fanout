/** Telemetry is untrusted. Escape text at the HTML boundary, not in canvas labels. */
export function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

/** A tooltip made exclusively of escaped text lines and fixed markup. */
export const tooltipLines = (lines: string[]) => lines.map(escapeHTML).join("<br/>");

export type TooltipPoint = { name?: string; seriesName?: string; axisValueLabel?: string; value?: unknown; data?: { value?: unknown } | unknown; };
export function htmlTooltip(format: (value: number, name?: string) => string, timeSeries = false) {
  return (params: TooltipPoint | TooltipPoint[]) => {
    const points = Array.isArray(params) ? params : [params];
    return tooltipLines([...(points[0]?.axisValueLabel ? [points[0].axisValueLabel] : []), ...points.map(point => {
      const value = point.value;
      const values = Array.isArray(value) ? timeSeries ? value.slice(1) : value : [value];
      return `${point.seriesName ?? ""}${point.name ? ` · ${point.name}` : ""}: ${values.map(v => typeof v === "number" ? format(v, point.seriesName) : String(v ?? "—")).join(" · ")}`;
    })]);
  };
}
