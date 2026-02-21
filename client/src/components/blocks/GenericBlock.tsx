export function GenericBlock({ type, data }: { type: string; data: unknown }) {
  const isObject =
    data !== null && typeof data === "object" && !Array.isArray(data);

  return (
    <div className="rounded-lg border border-border bg-muted/50 p-4">
      <span className="mb-2 inline-block rounded bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
        {type}
      </span>
      {isObject ? (
        <dl className="mt-2 space-y-1 text-sm">
          {Object.entries(data as Record<string, unknown>).map(
            ([key, value]) => (
              <div key={key} className="flex gap-2">
                <dt className="font-medium text-muted-foreground">{key}:</dt>
                <dd className="text-foreground">
                  {typeof value === "object"
                    ? JSON.stringify(value)
                    : String(value)}
                </dd>
              </div>
            ),
          )}
        </dl>
      ) : (
        <pre className="mt-2 overflow-x-auto text-xs text-muted-foreground">
          {JSON.stringify(data, null, 2)}
        </pre>
      )}
    </div>
  );
}
