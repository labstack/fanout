export function EmptyState({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="p-6 flex flex-col gap-1">
      <p className="text-ink text-sm font-medium">{title}</p>
      {hint && <p className="text-ink-2 text-[12.5px]">{hint}</p>}
    </div>
  );
}
