import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import type { ButtonHTMLAttributes } from "react";
import { cn } from "@/shared/lib/cn";

const button = cva(
  "inline-flex items-center justify-center gap-2 rounded-[var(--radius)] font-medium transition-colors duration-200 focus-visible:outline-2 disabled:opacity-50 disabled:pointer-events-none",
  {
    variants: {
      variant: {
        solid: "bg-accent text-accent-fg hover:opacity-90",
        ghost: "border border-line bg-raised text-ink-2 hover:text-ink hover:border-ink-3",
        quiet: "text-ink-2 hover:text-ink hover:bg-raised-2",
      },
      size: { sm: "h-8 px-3 text-[12.5px]", md: "h-9 px-4 text-sm" },
    },
    defaultVariants: { variant: "solid", size: "md" },
  },
);

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> &
  VariantProps<typeof button> & { asChild?: boolean };

export function Button({ className, variant, size, asChild, ...props }: ButtonProps) {
  const Comp = asChild ? Slot : "button";
  return <Comp className={cn(button({ variant, size }), className)} {...props} />;
}
