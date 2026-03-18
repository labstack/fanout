import type { Block } from "@/lib/types";
import { BlockRenderer } from "@/components/blocks/BlockRenderer";

const demoAction = (prompt: string) => alert(`Action: ${prompt}`);

// ---------- Comparison Block Prototype (not yet in block pipeline) ----------

interface CompareMetric {
  label: string;
  leftValue: number;
  rightValue: number;
  changePct: number;
  direction: "regression" | "improvement" | "stable";
  significant: boolean;
  unit?: string;
}

interface ComparisonData {
  mode: string;
  leftLabel: string;
  rightLabel: string;
  metrics: CompareMetric[];
  verdict: string;
}

function formatNum(v: number): string {
  if (Math.abs(v) >= 10000) return `${(v / 1000).toFixed(1)}k`;
  if (Math.abs(v) >= 100) return v.toLocaleString("en-US", { maximumFractionDigits: 0 });
  if (Math.abs(v) >= 1) return v.toLocaleString("en-US", { maximumFractionDigits: 1 });
  if (Math.abs(v) >= 0.01) return v.toLocaleString("en-US", { maximumFractionDigits: 2 });
  return v.toLocaleString("en-US", { maximumFractionDigits: 3 });
}

function ComparisonBlock({ data }: { data: ComparisonData }) {
  return (
    <div className="space-y-3">
      {/* Header bar */}
      <div className="flex items-center rounded-lg bg-muted/50 px-4 py-2.5">
        <div className="w-[130px] shrink-0" />
        <div className="w-[120px] shrink-0 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {data.leftLabel}
        </div>
        <div className="flex-1 text-center text-xs text-muted-foreground">Change</div>
        <div className="w-[120px] shrink-0 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {data.rightLabel}
        </div>
      </div>

      {/* Metric rows */}
      <div className="space-y-1.5">
        {data.metrics.map((m, i) => {
          const isRegression = m.direction === "regression";
          const isImprovement = m.direction === "improvement";
          const changeColor = isRegression
            ? "text-red-400"
            : isImprovement
              ? "text-emerald-400"
              : "text-zinc-500";
          const changeBg = isRegression
            ? "bg-red-500/10 border-red-500/30"
            : isImprovement
              ? "bg-emerald-500/10 border-emerald-500/30"
              : "bg-zinc-500/10 border-zinc-600/30";
          // Arrow based on whether the number went up or down, not direction
          const arrow = m.changePct > 5 ? "\u2191" : m.changePct < -5 ? "\u2193" : "\u2192";
          const rightColor = isRegression ? "text-red-400" : isImprovement ? "text-emerald-400" : "text-foreground";

          return (
            <div key={i} className={`flex items-center rounded-lg border px-4 py-3 transition-colors hover:bg-muted/30 ${
              isRegression ? "border-red-500/15" : isImprovement ? "border-emerald-500/15" : "border-border"
            }`}>
              {/* Label */}
              <div className="w-[130px] shrink-0">
                <span className="text-sm font-medium text-foreground">{m.label}</span>
                {m.unit && <span className="text-xs text-muted-foreground ml-1.5">{m.unit}</span>}
              </div>

              {/* Left value */}
              <div className="w-[120px] shrink-0 text-center">
                <span className="text-xl font-bold tabular-nums text-foreground">
                  {formatNum(m.leftValue)}
                </span>
              </div>

              {/* Change pill */}
              <div className="flex-1 flex items-center justify-center gap-2">
                <div className={`inline-flex items-center gap-1 rounded-full border px-3 py-1 text-xs font-bold tabular-nums ${changeBg} ${changeColor}`}>
                  <span className="text-[11px]">{arrow}</span>
                  <span>{m.changePct > 0 ? "+" : ""}{m.changePct.toFixed(0)}%</span>
                </div>
                {m.significant && (
                  <span className={`text-[9px] font-bold uppercase tracking-widest px-1.5 py-0.5 rounded ${
                    isRegression ? "text-red-400/80 bg-red-500/10" : isImprovement ? "text-emerald-400/80 bg-emerald-500/10" : "text-zinc-500 bg-muted"
                  }`} title="Statistically significant">
                    sig
                  </span>
                )}
              </div>

              {/* Right value */}
              <div className="w-[120px] shrink-0 text-center">
                <span className={`text-xl font-bold tabular-nums ${rightColor}`}>
                  {formatNum(m.rightValue)}
                </span>
              </div>
            </div>
          );
        })}
      </div>

      {/* Verdict */}
      {data.verdict && (
        <div className="flex items-start gap-2 text-sm text-muted-foreground border-t border-border pt-3">
          <span className="shrink-0 mt-0.5 text-amber-400">&#9670;</span>
          {data.verdict}
        </div>
      )}
    </div>
  );
}

const comparisonDemoData: ComparisonData = {
  mode: "time",
  leftLabel: "Before deploy (10:00\u201311:00)",
  rightLabel: "After deploy (11:00\u201312:00)",
  metrics: [
    { label: "P95 Latency", leftValue: 45, rightValue: 310, changePct: 588, direction: "regression", significant: true, unit: "ms" },
    { label: "Error Rate", leftValue: 0.3, rightValue: 7.2, changePct: 2300, direction: "regression", significant: true, unit: "%" },
    { label: "Throughput", leftValue: 3400, rightValue: 1200, changePct: -64, direction: "regression", significant: true, unit: "rpm" },
    { label: "P50 Latency", leftValue: 12, rightValue: 18, changePct: 50, direction: "stable", significant: false, unit: "ms" },
  ],
  verdict: "Significant regression across latency, errors, and throughput after deploy v2.3.1. P50 within normal variance.",
};

const blocks: { title: string; description: string; block: Block }[] = [
  {
    title: "Metrics",
    description: "KPI summary cards with status indicators",
    block: {
      type: "metrics",
      data: {
        items: [
          { label: "Throughput", value: 3420, unit: "rpm", status: "ok" },
          { label: "Error Rate", value: 4.2, unit: "%", status: "warning" },
          { label: "P95 Latency", value: 287, unit: "ms", status: "danger" },
          { label: "P50 Latency", value: 12, unit: "ms", status: "ok" },
          { label: "Active Services", value: 18, unit: "", status: "ok" },
          { label: "Failed Deploys", value: 2, unit: "", status: "danger" },
        ],
      },
    },
  },
  {
    title: "Table",
    description: "Tabular data with sortable columns and service drill-down",
    block: {
      type: "table",
      data: {
        columns: [
          { key: "service", label: "Service" },
          { key: "requests", label: "Requests", align: "right" },
          { key: "error_rate", label: "Error Rate", align: "right" },
          { key: "p95", label: "P95 (ms)", align: "right" },
          { key: "status", label: "Status" },
        ],
        rows: [
          { service: "checkout", requests: 12400, error_rate: "0.3%", p95: 45, status: "healthy" },
          { service: "payment", requests: 8200, error_rate: "1.2%", p95: 120, status: "degraded" },
          { service: "frontend", requests: 34000, error_rate: "0.1%", p95: 18, status: "healthy" },
          { service: "cart", requests: 15600, error_rate: "5.4%", p95: 310, status: "unhealthy" },
          { service: "recommendation", requests: 4200, error_rate: "0.0%", p95: 8, status: "healthy" },
          { service: "shipping", requests: 3100, error_rate: "0.5%", p95: 95, status: "healthy" },
        ],
      },
    },
  },
  {
    title: "Timeseries",
    description: "Multi-series line chart for trends over time",
    block: {
      type: "timeseries",
      data: {
        title: "Service Latency (P95) Over Time",
        yLabel: "ms",
        labels: ["14:00", "14:05", "14:10", "14:15", "14:20", "14:25", "14:30", "14:35", "14:40", "14:45", "14:50", "14:55"],
        series: [
          { label: "checkout", values: [42, 45, 43, 48, 120, 280, 310, 295, 180, 65, 48, 44] },
          { label: "payment", values: [110, 115, 108, 112, 118, 125, 190, 165, 130, 120, 115, 112] },
          { label: "frontend", values: [15, 16, 14, 18, 22, 35, 42, 38, 25, 18, 16, 15] },
        ],
      },
    },
  },
  {
    title: "Bar",
    description: "Ranked horizontal bar chart for comparisons",
    block: {
      type: "bar",
      data: {
        title: "Slowest Operations (P95)",
        yLabel: "ms",
        horizontal: true,
        bars: [
          { label: "POST /checkout", value: 310 },
          { label: "GET /cart", value: 185 },
          { label: "POST /payment", value: 120 },
          { label: "GET /products", value: 45 },
          { label: "GET /recommendations", value: 22 },
          { label: "GET /health", value: 3 },
        ],
      },
    },
  },
  {
    title: "Heatmap",
    description: "Latency distribution across time buckets",
    block: {
      type: "heatmap",
      data: {
        title: "Latency Distribution (checkout)",
        buckets: [5, 10, 25, 50, 100, 250, 500, 1000],
        times: ["14:00", "14:10", "14:20", "14:30", "14:40", "14:50"],
        values: [
          [80, 120, 45, 12, 3, 0, 0, 0],
          [75, 115, 50, 15, 5, 1, 0, 0],
          [40, 60, 80, 45, 30, 15, 5, 2],
          [20, 30, 50, 60, 55, 40, 25, 10],
          [55, 90, 65, 30, 12, 5, 1, 0],
          [78, 118, 48, 14, 4, 1, 0, 0],
        ],
      },
    },
  },
  {
    title: "Trace Waterfall",
    description: "Distributed trace visualization with span hierarchy",
    block: {
      type: "trace_waterfall",
      data: {
        spans: [
          { id: "s1", parent: null, service: "frontend-proxy", operation: "ingress", start: 0, duration: 125, status: "ok" },
          { id: "s2", parent: "s1", service: "frontend", operation: "GET /checkout", start: 2, duration: 120, status: "ok" },
          { id: "s3", parent: "s2", service: "cart", operation: "GetCart", start: 5, duration: 18, status: "ok" },
          { id: "s4", parent: "s3", service: "cart", operation: "redis GET", start: 7, duration: 3, status: "ok" },
          { id: "s5", parent: "s2", service: "product-catalog", operation: "ListProducts", start: 25, duration: 12, status: "ok" },
          { id: "s6", parent: "s2", service: "checkout", operation: "PlaceOrder", start: 40, duration: 78, status: "error" },
          { id: "s7", parent: "s6", service: "payment", operation: "Charge", start: 42, duration: 65, status: "error" },
          { id: "s8", parent: "s7", service: "payment", operation: "stripe.charges.create", start: 44, duration: 60, status: "error" },
          { id: "s9", parent: "s6", service: "email", operation: "SendConfirmation", start: 110, duration: 5, status: "ok" },
        ],
      },
    },
  },
  {
    title: "Topology",
    description: "Service dependency graph with health status",
    block: {
      type: "topology",
      data: {
        nodes: [
          { id: "frontend-proxy", status: "healthy", rpm: 3400, p95: 18, errors: 0.1 },
          { id: "frontend", status: "healthy", rpm: 3200, p95: 15, errors: 0.05 },
          { id: "checkout", status: "degraded", rpm: 800, p95: 120, errors: 2.1 },
          { id: "cart", status: "unhealthy", rpm: 1500, p95: 310, errors: 5.4 },
          { id: "payment", status: "degraded", rpm: 600, p95: 95, errors: 1.2 },
          { id: "product-catalog", status: "healthy", rpm: 2800, p95: 8, errors: 0 },
          { id: "recommendation", status: "healthy", rpm: 400, p95: 6, errors: 0 },
          { id: "shipping", status: "healthy", rpm: 300, p95: 22, errors: 0 },
        ],
        edges: [
          { source: "frontend-proxy", target: "frontend", rpm: 3200, errorRate: 0.05 },
          { source: "frontend", target: "checkout", rpm: 800, errorRate: 2.1 },
          { source: "frontend", target: "cart", rpm: 1500, errorRate: 5.4 },
          { source: "frontend", target: "product-catalog", rpm: 1200, errorRate: 0 },
          { source: "frontend", target: "recommendation", rpm: 400, errorRate: 0 },
          { source: "checkout", target: "payment", rpm: 600, errorRate: 1.2 },
          { source: "checkout", target: "shipping", rpm: 300, errorRate: 0 },
          { source: "checkout", target: "cart", rpm: 200, errorRate: 0 },
          { source: "recommendation", target: "product-catalog", rpm: 400, errorRate: 0 },
        ],
      },
    },
  },
  {
    title: "Flame Graph",
    description: "Aggregated span breakdown showing where time is spent",
    block: {
      type: "flame_graph",
      data: {
        frames: [
          { name: "GET /checkout", depth: 0, x: 0, w: 1, self: 0.05, total: 1, service: "frontend" },
          { name: "GetCart", depth: 1, x: 0, w: 0.15, self: 0.08, total: 0.15, service: "cart" },
          { name: "redis GET", depth: 2, x: 0, w: 0.07, self: 0.07, total: 0.07, service: "cart" },
          { name: "ListProducts", depth: 1, x: 0.15, w: 0.1, self: 0.1, total: 0.1, service: "product-catalog" },
          { name: "PlaceOrder", depth: 1, x: 0.25, w: 0.65, self: 0.1, total: 0.65, service: "checkout" },
          { name: "Charge", depth: 2, x: 0.25, w: 0.5, self: 0.05, total: 0.5, service: "payment" },
          { name: "stripe.create", depth: 3, x: 0.25, w: 0.45, self: 0.45, total: 0.45, service: "payment" },
          { name: "SendEmail", depth: 2, x: 0.75, w: 0.05, self: 0.05, total: 0.05, service: "email" },
          { name: "GetRecommendations", depth: 1, x: 0.9, w: 0.05, self: 0.03, total: 0.05, service: "recommendation" },
        ],
      },
    },
  },
  {
    title: "Sankey",
    description: "Request flow between services showing traffic distribution",
    block: {
      type: "sankey",
      data: {
        nodes: [
          { id: "load-gen", label: "Load Generator", rpm: 3400, status: "healthy" },
          { id: "proxy", label: "Frontend Proxy", rpm: 3400, status: "healthy" },
          { id: "frontend", label: "Frontend", rpm: 3200, status: "healthy" },
          { id: "checkout", label: "Checkout", rpm: 800, status: "degraded" },
          { id: "cart", label: "Cart", rpm: 1500, status: "unhealthy" },
          { id: "catalog", label: "Product Catalog", rpm: 2800, status: "healthy" },
          { id: "payment", label: "Payment", rpm: 600, status: "degraded" },
          { id: "shipping", label: "Shipping", rpm: 300, status: "healthy" },
        ],
        links: [
          { source: "load-gen", target: "proxy", value: 3400 },
          { source: "proxy", target: "frontend", value: 3200 },
          { source: "frontend", target: "checkout", value: 800 },
          { source: "frontend", target: "cart", value: 1500 },
          { source: "frontend", target: "catalog", value: 1200 },
          { source: "checkout", target: "payment", value: 600 },
          { source: "checkout", target: "shipping", value: 300 },
          { source: "checkout", target: "cart", value: 200 },
        ],
      },
    },
  },
  {
    title: "Dependency Matrix",
    description: "NxN service health grid showing inter-service metrics",
    block: {
      type: "dep_matrix",
      data: {
        services: ["frontend", "checkout", "cart", "payment", "shipping"],
        cells: [
          { from: "frontend", to: "checkout", errorRate: 0.02, rpm: 800, p95: 120 },
          { from: "frontend", to: "cart", errorRate: 0.054, rpm: 1500, p95: 310 },
          { from: "checkout", to: "payment", errorRate: 0.012, rpm: 600, p95: 95 },
          { from: "checkout", to: "shipping", errorRate: 0, rpm: 300, p95: 22 },
          { from: "checkout", to: "cart", errorRate: 0, rpm: 200, p95: 15 },
        ],
      },
    },
  },
  {
    title: "Endpoints",
    description: "Per-endpoint performance breakdown with latency percentiles",
    block: {
      type: "endpoints",
      data: {
        endpoints: [
          { method: "POST", path: "/api/checkout", rpm: 420, p50: 45, p95: 310, p99: 890, errorRate: 0.032, status: "degraded" },
          { method: "GET", path: "/api/cart", rpm: 1500, p50: 8, p95: 42, p99: 120, errorRate: 0.054, status: "unhealthy" },
          { method: "POST", path: "/api/payment/charge", rpm: 380, p50: 55, p95: 120, p99: 450, errorRate: 0.012, status: "degraded" },
          { method: "GET", path: "/api/products", rpm: 2800, p50: 3, p95: 12, p99: 35, errorRate: 0, status: "healthy" },
          { method: "GET", path: "/api/recommendations", rpm: 400, p50: 2, p95: 8, p99: 18, errorRate: 0, status: "healthy" },
          { method: "POST", path: "/api/shipping/quote", rpm: 300, p50: 12, p95: 45, p99: 95, errorRate: 0, status: "healthy" },
        ],
      },
    },
  },
  {
    title: "Correlation",
    description: "Multi-signal correlation showing latency vs errors vs throughput",
    block: {
      type: "correlation",
      data: {
        times: ["14:00", "14:05", "14:10", "14:15", "14:20", "14:25", "14:30", "14:35", "14:40", "14:45", "14:50", "14:55"],
        panels: [
          {
            label: "P95 Latency (ms)",
            color: "#8884d8",
            values: [42, 45, 43, 48, 120, 280, 310, 295, 180, 65, 48, 44],
            baseline: 50,
            markers: [
              { t: "14:15", label: "Deploy v2.3.1", severity: "warning" },
              { t: "14:40", label: "Rollback", severity: "ok" },
            ],
          },
          {
            label: "Error Rate (%)",
            color: "#ef4444",
            values: [0.3, 0.2, 0.4, 0.5, 2.1, 5.8, 7.2, 6.5, 3.1, 0.8, 0.3, 0.2],
            baseline: 1.0,
          },
          {
            label: "Throughput (rpm)",
            color: "#82ca9d",
            values: [3400, 3380, 3420, 3350, 2800, 1900, 1200, 1500, 2600, 3200, 3380, 3400],
            baseline: 3400,
          },
        ],
      },
    },
  },
  {
    title: "Logs",
    description: "Log entries with severity badges, service names, and trace correlation",
    block: {
      type: "logs",
      data: {
        entries: [
          { time: "2026-03-18T14:20:00.123Z", severity: "ERROR", service: "payment", body: "Stripe API timeout after 30s — charge failed for order ord-8842", traceId: "abc123def456789a" },
          { time: "2026-03-18T14:20:01.456Z", severity: "WARN", service: "checkout", body: "Payment retry #2 for order ord-8842, backing off 500ms", traceId: "abc123def456789a" },
          { time: "2026-03-18T14:20:02.789Z", severity: "ERROR", service: "cart", body: "Redis connection pool exhausted — 0/50 available connections" },
          { time: "2026-03-18T14:20:03.012Z", severity: "INFO", service: "frontend", body: "GET /api/cart returned 503 — upstream unavailable", traceId: "xyz789abc0123456" },
          { time: "2026-03-18T14:20:04.345Z", severity: "WARN", service: "cart", body: "Redis reconnecting — attempt 3/5" },
          { time: "2026-03-18T14:20:05.678Z", severity: "INFO", service: "payment", body: "Stripe API recovered — charge succeeded for order ord-8843", traceId: "def456ghi7890123" },
          { time: "2026-03-18T14:20:06.901Z", severity: "DEBUG", service: "recommendation", body: "Cache miss for user u-12345, fetching from product-catalog", traceId: "jkl012mno3456789" },
          { time: "2026-03-18T14:20:07.234Z", severity: "ERROR", service: "payment", body: "Invalid card number format for order ord-8844", traceId: "pqr678stu9012345" },
          { time: "2026-03-18T14:20:08.567Z", severity: "FATAL", service: "cart", body: "Redis connection permanently lost — circuit breaker OPEN" },
          { time: "2026-03-18T14:20:09.890Z", severity: "TRACE", service: "product-catalog", body: "gRPC ListProducts invoked with page_size=20, cursor=abc" },
        ],
      },
    },
  },
  {
    title: "Text",
    description: "Markdown text block for narrative content",
    block: {
      type: "text",
      data: {
        content:
          "## Analysis Summary\n\nThe **checkout** service experienced a latency spike between 14:15–14:35 UTC, " +
          "correlating with deploy `v2.3.1`. Error rate peaked at **7.2%** (baseline: 0.3%). " +
          "Root cause: the payment service's connection to Stripe timed out due to a misconfigured retry policy.\n\n" +
          "**Rollback** at 14:40 restored normal performance within 10 minutes.",
      },
    },
  },
];

export function DemoPage() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-50 border-b border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
        <div className="max-w-5xl mx-auto flex items-center justify-between px-6 py-3">
          <div className="flex items-center gap-3">
            <h1 className="text-lg font-semibold">Block Components</h1>
            <span className="text-xs text-muted-foreground bg-muted px-2 py-0.5 rounded-full">
              {blocks.length} blocks
            </span>
          </div>
          <a href="/" className="text-sm text-muted-foreground hover:text-foreground transition-colors">
            Back to Chat
          </a>
        </div>
      </header>

      <main className="max-w-5xl mx-auto px-6 py-8 space-y-12">
        {blocks.map(({ title, description, block }, i) => (
          <section key={i} id={title.toLowerCase().replace(/\s+/g, "-")}>
            <div className="mb-4">
              <div className="flex items-center gap-3 mb-1">
                <h2 className="text-xl font-semibold">{title}</h2>
                <code className="text-xs text-muted-foreground bg-muted px-2 py-0.5 rounded font-mono">
                  {block.type}
                </code>
              </div>
              <p className="text-sm text-muted-foreground">{description}</p>
            </div>
            <div className="rounded-xl border border-border bg-card p-4">
              <BlockRenderer block={block} onAction={demoAction} />
            </div>
          </section>
        ))}

        {/* Prototype: Comparison block (not yet in block pipeline) */}
        <section id="comparison">
          <div className="mb-4">
            <div className="flex items-center gap-3 mb-1">
              <h2 className="text-xl font-semibold">Comparison</h2>
              <code className="text-xs text-amber-400/80 bg-amber-500/10 border border-amber-500/20 px-2 py-0.5 rounded font-mono">
                prototype
              </code>
            </div>
            <p className="text-sm text-muted-foreground">Before/after comparison with change direction, percentages, and statistical significance</p>
          </div>
          <div className="rounded-xl border border-border bg-card p-4">
            <ComparisonBlock data={comparisonDemoData} />
          </div>
        </section>
      </main>
    </div>
  );
}
