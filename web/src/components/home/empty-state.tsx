import { useEffect, useState } from "react";
import { Check, Copy, Radio, ShieldCheck } from "lucide-react";
import { api } from "@/api/client";

interface IngestResponse {
  suggested_endpoint: string;
  header_name: string;
}

function CopyButton({ text, label }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      globalThis.setTimeout(() => setCopied(false), 1200);
    } catch {
      /* clipboard unavailable — no-op */
    }
  };

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={label ?? "Copy"}
      className="inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-1 font-mono text-[10.5px] text-muted-foreground transition-colors hover:bg-surface-3 hover:text-foreground"
    >
      {copied ? (
        <Check className="size-3 text-healthy" />
      ) : (
        <Copy className="size-3" />
      )}
      {label && <span>{copied ? "Copied" : label}</span>}
    </button>
  );
}

interface Props {
  /** When set, hints the user that present-but-stale data may be hidden by the
   *  current window — shown alongside the welcome card. */
  windowLabel?: string;
}

export function EmptyState({ windowLabel }: Props) {
  const [ingest, setIngest] = useState<IngestResponse | null>(null);

  useEffect(() => {
    api<IngestResponse>("/api/settings/ingest")
      .then(setIngest)
      .catch(() => setIngest(null));
  }, []);

  const endpoint = ingest?.suggested_endpoint ?? "127.0.0.1:4317";
  const headerName = ingest?.header_name ?? "x-fanout-ingest-token";

  const lines = [
    { k: "OTEL_EXPORTER_OTLP_ENDPOINT", v: endpoint },
    { k: "OTEL_EXPORTER_OTLP_PROTOCOL", v: "grpc" },
    { k: "OTEL_EXPORTER_OTLP_HEADERS", v: `${headerName}=<YOUR_TOKEN>` },
  ];
  const allText = lines.map((l) => `${l.k}=${l.v}`).join("\n");

  return (
    <div className="flex min-h-[55vh] items-center justify-center px-4">
      <div className="fade-up w-full max-w-2xl space-y-6 text-center">
        <div className="inline-flex size-12 items-center justify-center rounded-xl border border-border bg-surface-2">
          <Radio className="size-5 text-primary" />
        </div>
        <div className="mx-auto max-w-md space-y-2">
          <h1 className="font-heading text-xl font-semibold text-foreground">
            Welcome to Fanout
          </h1>
          <p className="text-sm leading-6 text-muted-foreground">
            Point a collector at this endpoint — data will appear here shortly.
          </p>
        </div>

        <div className="overflow-hidden rounded-lg border border-border bg-surface-2/50 text-left">
          <div className="flex items-center justify-between border-b border-border/60 px-3 py-2">
            <span className="detail-label">Collector configuration</span>
            <CopyButton text={allText} label="Copy all" />
          </div>
          <div className="space-y-0.5 px-3 py-3 font-mono text-[12.5px] leading-6">
            {lines.map((l) => (
              <div key={l.k} className="group flex items-center gap-2">
                <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap">
                  <span className="text-muted-foreground">{l.k}</span>
                  <span className="text-foreground">={l.v}</span>
                </code>
                <span className="opacity-0 transition-opacity group-hover:opacity-100">
                  <CopyButton text={`${l.k}=${l.v}`} />
                </span>
              </div>
            ))}
          </div>
        </div>

        {windowLabel && (
          <p className="mx-auto max-w-md text-xs text-muted-foreground">
            Already sending data? Nothing arrived in the last{" "}
            <span className="font-mono text-foreground/80">{windowLabel}</span> —
            try a wider time range above.
          </p>
        )}

        <div className="flex items-center justify-center gap-2 text-xs text-muted-foreground">
          <ShieldCheck className="size-3.5 text-healthy" />
          <span>Rotate the token from settings if you lose it</span>
        </div>
      </div>
    </div>
  );
}
