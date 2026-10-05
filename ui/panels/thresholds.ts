import type { Status, Threshold } from "./types";

const rank: Record<Status, number> = { ok: 0, warn: 1, bad: 2 };

/** Infer which direction worsens health from threshold values and severity. */
export function thresholdDirection(thresholds?: Threshold[]): "lower" | "higher" | undefined {
  if (!thresholds?.length) return undefined;
  const ordered = [...thresholds].sort((a, b) => rank[a.status] - rank[b.status]);
  const best = ordered[0];
  const worst = ordered[ordered.length - 1];
  if (rank[worst.status] > rank[best.status] && worst.value !== best.value) {
    return worst.value > best.value ? "lower" : "higher";
  }
  // A lone unhealthy threshold (or equal-severity limits) is crossed upward.
  return "lower";
}

/** The worst status whose threshold the value has crossed, in the direction
 *  that is worse: up for "lower is better", down for "higher is better". */
export function statusFor(value: number | null, thresholds?: Threshold[], better?: "lower" | "higher"): Status | null {
  if (value === null || !thresholds || thresholds.length === 0) return null;
  better ??= thresholdDirection(thresholds);
  let status: Status = "ok";
  for (const t of thresholds) {
    const crossed = better === "lower" ? value >= t.value : value <= t.value;
    if (crossed && rank[t.status] > rank[status]) status = t.status;
  }
  return status;
}
