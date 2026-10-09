import type { DashboardSpec } from "./types";

export const panelContent = (spec: DashboardSpec) => ({
  panels: spec.panels.map(({ grid: _grid, ...panel }) => panel),
  variables: spec.variables,
  time: spec.time,
  annotations: spec.annotations,
});
