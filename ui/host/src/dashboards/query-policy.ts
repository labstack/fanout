import type { DashboardSpec } from "../../../panels/types";
import { ApiError } from "./api";

export const retryQuery = (count: number, error: Error) =>
  !(error instanceof ApiError && error.status < 500) && count < 2;

export const retryPanelQuery = (count: number, error: Error) =>
  !(error instanceof ApiError && error.status === 504) && retryQuery(count, error);

export const panelContent = (spec: DashboardSpec) => ({
  panels: spec.panels.map(({ grid: _grid, ...panel }) => panel),
  variables: spec.variables,
  time: spec.time,
  annotations: spec.annotations,
});
