import { useEffect, useState } from "react";
import { Radio, ShieldCheck, ShieldOff } from "lucide-react";
import { api } from "@/api/client";

interface IngestResponse {
  token_required: boolean;
  suggested_endpoint: string;
  header_name: string;
}

export function EmptyState() {
  const [ingest, setIngest] = useState<IngestResponse | null>(null);

  useEffect(() => {
    api<IngestResponse>("/api/settings/ingest")
      .then(setIngest)
      .catch(() => setIngest(null));
  }, []);

  const endpoint = ingest?.suggested_endpoint ?? "127.0.0.1:4317";
  const tokenRequired = ingest?.token_required ?? false;

  return (
    <div className="flex items-center justify-center min-h-[60vh]">
      <div className="text-center max-w-md space-y-6 fade-up">
        <div className="inline-flex items-center justify-center w-12 h-12 rounded-xl bg-surface-2 border border-border">
          <Radio className="h-5 w-5 text-primary" />
        </div>
        <div className="space-y-2">
          <h1 className="font-heading text-xl font-semibold text-foreground">
            Welcome to Fanout
          </h1>
          <p className="text-sm text-muted-foreground leading-6">
            Point a collector at this endpoint — data will appear here shortly.
          </p>
        </div>
        <div className="stat-card text-left space-y-2">
          <div className="detail-label">Collector configuration</div>
          <div className="space-y-1 text-sm mono text-foreground/80">
            <div>
              <span className="text-muted-foreground">OTEL_EXPORTER_OTLP_ENDPOINT</span>
              <span className="text-primary">={endpoint}</span>
            </div>
            <div>
              <span className="text-muted-foreground">OTEL_EXPORTER_OTLP_PROTOCOL</span>
              <span className="text-primary">=grpc</span>
            </div>
            {tokenRequired && (
              <div>
                <span className="text-muted-foreground">OTEL_EXPORTER_OTLP_HEADERS</span>
                <span className="text-primary">={ingest?.header_name}=&lt;YOUR_TOKEN&gt;</span>
              </div>
            )}
          </div>
        </div>
        {tokenRequired ? (
          <div className="flex items-center justify-center gap-2 text-xs text-muted-foreground">
            <ShieldCheck className="h-3.5 w-3.5 text-healthy" />
            <span>Ingest requires a token (rotate from settings)</span>
          </div>
        ) : (
          <div className="flex items-center justify-center gap-2 text-xs text-muted-foreground">
            <ShieldOff className="h-3.5 w-3.5 text-degraded" />
            <span>Ingest is open — rotate a token from settings to require auth</span>
          </div>
        )}
      </div>
    </div>
  );
}
