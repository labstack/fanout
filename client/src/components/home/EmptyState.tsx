import { Radio } from "lucide-react";

export function EmptyState() {
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
            Point your OTLP exporter here to get started.
          </p>
        </div>
        <div className="stat-card text-left space-y-2">
          <div className="detail-label">Configuration</div>
          <div className="space-y-1 text-sm mono text-foreground/80">
            <div>
              <span className="text-muted-foreground">OTEL_EXPORTER_OTLP_ENDPOINT</span>
              <span className="text-primary">=localhost:4317</span>
            </div>
            <div>
              <span className="text-muted-foreground">OTEL_EXPORTER_OTLP_PROTOCOL</span>
              <span className="text-primary">=grpc</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
