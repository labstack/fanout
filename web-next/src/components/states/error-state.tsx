import { Button } from "@/shared/ui/button";

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="p-6 flex flex-col items-start gap-3">
      <p className="text-crit-text text-sm">{message}</p>
      {onRetry && <Button variant="ghost" size="sm" onClick={onRetry}>Retry</Button>}
    </div>
  );
}
