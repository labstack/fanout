import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

interface ErrorStateProps {
  error?: Error | string | null;
  resetErrorBoundary?: () => void;
  retry?: () => void;
  className?: string;
}

export function ErrorState({
  error,
  resetErrorBoundary,
  retry,
  className,
}: ErrorStateProps) {
  const message =
    typeof error === "string"
      ? error
      : (error?.message ?? "Something went wrong");
  const reset = resetErrorBoundary ?? retry;

  return (
    <div
      role="alert"
      className={cn(
        "flex flex-col items-center justify-center rounded-lg border border-danger/30 bg-danger/5 p-8 text-center",
        className,
      )}
    >
      <p className="font-mono text-xs uppercase tracking-[0.16em] text-danger">
        Error
      </p>
      <p className="mt-2 max-w-md text-sm text-foreground/80">{message}</p>
      {reset ? (
        <Button
          variant="outline"
          size="sm"
          className="mt-4"
          onClick={reset}
        >
          Try again
        </Button>
      ) : null}
    </div>
  );
}
