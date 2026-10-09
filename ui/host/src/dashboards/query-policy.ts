import { ApiError } from "./api";

export const retryQuery = (count: number, error: Error) =>
  !(error instanceof ApiError && error.status < 500) && count < 2;

export const retryPanelQuery = (count: number, error: Error) =>
  !(error instanceof ApiError && error.code === "timeout") && retryQuery(count, error);
