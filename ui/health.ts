/* The thresholds a number is graded against, mirroring classify() in
   internal/observability/service.go. They live here so a cell can be coloured
   by the signal it actually shows: a service can be unhealthy on latency alone,
   and colouring its error rate by that verdict paints "0.00%" red on a row with
   no errors at all. The row badge still carries the combined verdict. */

export const latencyDegradedMS = 750;
export const latencyUnhealthyMS = 2000;
export const errorRateDegraded = 0.01;
export const errorRateUnhealthy = 0.05;

/** Mantine colour for a latency figure, or undefined to leave it unstyled. */
export function latencyTone(ms: number): string | undefined {
  if (ms >= latencyUnhealthyMS) return "bad";
  if (ms >= latencyDegradedMS) return "warn";
  return undefined;
}

/** Mantine colour for an error rate. Dimmed rather than green: a rate of zero
 *  is the resting state, and it should recede rather than announce itself. */
export function errorRateTone(rate: number): string {
  if (rate >= errorRateUnhealthy) return "bad";
  if (rate >= errorRateDegraded) return "warn";
  return "dimmed";
}
