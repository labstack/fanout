export function LoadingState({ label = "Loading…" }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="p-6 text-ink-2 text-sm">
      {label}
    </div>
  );
}
