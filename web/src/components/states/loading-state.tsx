import { cn } from "@/lib/utils";
import { Skeleton } from "@/components/ui/skeleton";

interface LoadingStateProps {
  rows?: number;
  className?: string;
}

export function LoadingState({ rows = 4, className }: LoadingStateProps) {
  return (
    <div
      className={cn("space-y-3", className)}
      role="status"
      aria-busy="true"
      aria-label="Loading"
    >
      <Skeleton className="h-8 w-1/3" />
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className="h-12 w-full" />
      ))}
    </div>
  );
}
