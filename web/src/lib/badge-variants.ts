import type { BadgeProps } from "@/components/ui/badge";

type Variant = NonNullable<BadgeProps["variant"]>;

/** Map a service health value to a canonical badge variant. */
export function serviceStatusVariant(
  status: "healthy" | "degraded" | "unhealthy" | string,
): Variant {
  switch (status) {
    case "healthy":
      return "success";
    case "degraded":
      return "warning";
    case "unhealthy":
      return "danger";
    default:
      return "neutral";
  }
}

/** Map an alert state to a canonical badge variant. */
export function alertStateVariant(
  state: "firing" | "recovered" | "pending" | string,
): Variant {
  switch (state) {
    case "firing":
      return "danger";
    case "pending":
      return "warning";
    case "recovered":
      return "success";
    default:
      return "neutral";
  }
}

/** Map an HTTP-status range to a canonical badge variant. */
export function httpStatusVariant(status: number): Variant {
  if (status >= 500) return "danger";
  if (status >= 400) return "warning";
  if (status >= 300) return "info";
  if (status >= 200) return "success";
  return "neutral";
}
