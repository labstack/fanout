import { z } from "zod";

// Matches the wire shape of GET /api/overview, i.e. internal/api.overviewResponse
// (internal/api/overview_response.go), NOT service.OverviewResult directly — that
// in-process type carries no JSON tags and is remapped by toOverviewResponse().
// Hand-written now; a codegen step replaces this in the contract plan.

export const overviewHealthSchema = z.object({
  score: z.number(),
  total_services: z.number(),
  // by_status has no nil-guard in toOverviewResponse, so it can serialize as
  // `null` when OverviewHealth was never populated (e.g. Include didn't ask
  // for health).
  by_status: z.record(z.string(), z.number()).nullable(),
  throughput_per_min: z.number(),
  global_error_rate: z.number(),
  global_p95_ms: z.number(),
});

export const overviewServiceSchema = z.object({
  service: z.string(),
  status: z.string(),
  health_score: z.number(),
  requests: z.number(),
  traffic_per_min: z.number(),
  error_rate: z.number(),
  p50_ms: z.number(),
  p95_ms: z.number(),
  // omitempty on the Go side (SparklineTraffic []float64)
  sparkline_traffic: z.array(z.number()).optional(),
});

export const topErrorSchema = z.object({
  message: z.string(),
  count: z.number(),
});

export const overviewIncidentSchema = z.object({
  service: z.string(),
  status: z.string(),
  health_score: z.number(),
  error_rate: z.number(),
  p95_ms: z.number(),
  traffic_per_min: z.number(),
  started_at: z.string().optional(),
  lifecycle: z.string(),
  sparkline_error_rate: z.array(z.number()),
  top_errors: z.array(topErrorSchema).optional(),
  related: z.array(z.string()).optional(),
});

export const activityBucketSchema = z.object({
  t: z.string(),
  spans: z.number(),
  error_rate: z.number(),
});

export const overviewActivitySchema = z.object({
  buckets: z.array(activityBucketSchema),
});

export const recentErrorSchema = z.object({
  service: z.string(),
  message: z.string(),
  count: z.number(),
});

export const overviewAlertSchema = z.object({
  rule: z.string(),
  service: z.string(),
  state: z.string(),
  value: z.number(),
  fired_at: z.string(),
});

export const alertsStatusSchema = z.enum(["ok", "unavailable", "disabled"]);

export const overviewAlertsSchema = z.object({
  status: alertsStatusSchema,
  items: z.array(overviewAlertSchema),
});

export const overviewSchema = z.object({
  health: overviewHealthSchema,
  services: z.array(overviewServiceSchema),
  incidents: z.array(overviewIncidentSchema),
  activity: overviewActivitySchema,
  recent_errors: z.array(recentErrorSchema),
  recent_errors_unavailable: z.boolean(),
  alerts: overviewAlertsSchema,
});

export type Overview = z.infer<typeof overviewSchema>;
export type OverviewHealth = z.infer<typeof overviewHealthSchema>;
export type OverviewService = z.infer<typeof overviewServiceSchema>;
export type OverviewIncident = z.infer<typeof overviewIncidentSchema>;
export type OverviewActivity = z.infer<typeof overviewActivitySchema>;
export type OverviewAlert = z.infer<typeof overviewAlertSchema>;
