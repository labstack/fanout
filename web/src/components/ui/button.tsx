import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";
import type { CanonicalVariantMap } from "@/lib/variants";

const buttonVariantClasses = {
  default: "bg-primary text-primary-foreground hover:bg-primary/90",
  secondary: "bg-secondary text-secondary-foreground hover:bg-surface-2",
  success: "bg-success text-success-foreground hover:bg-success/90",
  danger: "bg-danger text-danger-foreground hover:bg-danger/90",
  warning: "bg-warning text-warning-foreground hover:bg-warning/90",
  info: "bg-info text-info-foreground hover:bg-info/90",
  neutral: "bg-muted text-muted-foreground hover:bg-surface-2",
  outline: "border border-border bg-transparent hover:bg-surface-2 text-foreground",
  ghost: "hover:bg-surface-2 text-foreground",
  link: "text-primary underline-offset-4 hover:underline",
} as const satisfies CanonicalVariantMap;

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: buttonVariantClasses,
      size: {
        default: "h-9 px-4 py-2",
        sm: "h-8 rounded-md px-3 text-xs",
        lg: "h-10 rounded-md px-6",
        icon: "h-9 w-9",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return (
      <Comp
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        {...props}
      />
    );
  },
);
Button.displayName = "Button";

export { buttonVariants };
