import type * as React from "react";
import { cn } from "@/lib/utils";

interface PageContainerProps {
  children: React.ReactNode;
  className?: string;
  /** When true, suppress the entry fade animation (useful for chat-style layouts) */
  noFade?: boolean;
}

export function PageContainer({
  children,
  className,
  noFade = false,
}: PageContainerProps) {
  return (
    <div
      className={cn(
        "max-w-[1400px] mx-auto px-4 sm:px-6 pt-6 pb-20",
        !noFade && "fade-up",
        className,
      )}
    >
      {children}
    </div>
  );
}
