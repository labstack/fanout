import { cn } from "@/lib/utils";
import { Badge, type BadgeProps } from "@/components/ui/badge";

interface StatusBadgeProps extends Omit<BadgeProps, "variant"> {
  variant: "success" | "danger" | "warning" | "info" | "neutral";
  dot?: boolean;
}

const dotColor: Record<StatusBadgeProps["variant"], string> = {
  success: "bg-success",
  danger: "bg-danger",
  warning: "bg-warning",
  info: "bg-info",
  neutral: "bg-muted-foreground",
};

export function StatusBadge({
  variant,
  dot = true,
  className,
  children,
  ...props
}: StatusBadgeProps) {
  return (
    <Badge
      variant={variant}
      className={cn("gap-1.5 font-mono uppercase tracking-wide", className)}
      {...props}
    >
      {dot ? (
        <span
          aria-hidden="true"
          className={cn("size-1.5 rounded-full", dotColor[variant])}
        />
      ) : null}
      {children}
    </Badge>
  );
}
