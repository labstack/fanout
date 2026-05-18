import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";
import type { CanonicalVariantMap } from "@/lib/variants";

const badgeVariantClasses = {
  default: "border-transparent bg-primary text-primary-foreground",
  secondary: "border-transparent bg-secondary text-secondary-foreground",
  success: "border-transparent bg-success/15 text-success",
  danger: "border-transparent bg-danger/15 text-danger",
  warning: "border-transparent bg-warning/15 text-warning",
  info: "border-transparent bg-info/15 text-info",
  neutral: "border-transparent bg-muted text-muted-foreground",
  outline: "border-border text-foreground",
  ghost: "border-transparent text-foreground",
  link: "border-transparent text-primary underline-offset-4 hover:underline",
} as const satisfies CanonicalVariantMap;

const badgeVariants = cva(
  "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors focus:outline-none focus:ring-2 focus:ring-ring/40",
  {
    variants: { variant: badgeVariantClasses },
    defaultVariants: { variant: "default" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <div className={cn(badgeVariants({ variant }), className)} {...props} />
  );
}

export { badgeVariants };
