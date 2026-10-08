import { tooltipLines } from "./escape";

export function performanceAxisTooltip(params: { seriesName: string; axisValueLabel: string; value: number }[], format: (value: number, series: string) => string): string {
  return tooltipLines([params[0]?.axisValueLabel ?? "", ...params.map(p => `${p.seriesName}: ${format(p.value, p.seriesName)}`)]);
}
export const performanceHeatTooltip = (service: string, duration: string) => tooltipLines([service, duration]);
export const topologyMatrixTooltip = (caller: string, callee: string, calls: string, duration: string, percent: string) => tooltipLines([`${caller} → ${callee}`, `${calls} calls · ${duration}`, `${percent} errors`]);
