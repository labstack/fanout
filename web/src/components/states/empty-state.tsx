import type * as React from "react";
import { cn } from "@/lib/utils";

interface EmptyStateProps {
  title: string;
  description?: string;
  icon?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}

export function EmptyState({
  title,
  description,
  icon,
  action,
  className,
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center rounded-lg border border-dashed border-border/60 bg-card/30 p-10 text-center",
        className,
      )}
    >
      {icon ? (
        <div className="mb-3 text-muted-foreground" aria-hidden="true">
          {icon}
        </div>
      ) : null}
      <p className="font-mono text-xs uppercase tracking-[0.16em] text-muted-foreground">
        {title}
      </p>
      {description ? (
        <p className="mt-2 max-w-sm text-sm text-muted-foreground/80">
          {description}
        </p>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
