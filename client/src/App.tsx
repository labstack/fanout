import type { Block } from "@/lib/types";
import { BlockRenderer } from "@/components/blocks/BlockRenderer";

const testBlocks: Block[] = [
  {
    type: "text",
    data: {
      content:
        "## Service Health\n\nThe **checkout** service is experiencing elevated error rates.\n\n- P95 latency: `320ms`\n- Error rate: **4.2%**\n- Throughput: 1,200 rpm",
    },
  },
  {
    type: "metrics",
    data: {
      items: [
        { label: "Throughput", value: 1200, unit: "rpm", status: "ok" as const },
        { label: "P95 Latency", value: 320, unit: "ms", status: "warning" as const },
        { label: "Error Rate", value: 4.2, unit: "%", status: "danger" as const },
        { label: "Uptime", value: 99.97, unit: "%", status: "ok" as const },
        { label: "Saturation", value: 78, unit: "%", status: "warning" as const },
      ],
    },
  },
  {
    type: "table",
    data: {
      columns: [
        { key: "endpoint", label: "Endpoint", align: "left" },
        { key: "method", label: "Method", align: "left" },
        { key: "rpm", label: "RPM", align: "right" },
        { key: "p50", label: "P50 (ms)", align: "right" },
        { key: "p95", label: "P95 (ms)", align: "right" },
        { key: "errorRate", label: "Error %", align: "right" },
      ],
      rows: [
        { endpoint: "/api/checkout", method: "POST", rpm: 420, p50: 45, p95: 320, errorRate: 4.2 },
        { endpoint: "/api/products", method: "GET", rpm: 1800, p50: 12, p95: 48, errorRate: 0.1 },
        { endpoint: "/api/cart", method: "PUT", rpm: 650, p50: 28, p95: 95, errorRate: 1.3 },
        { endpoint: "/api/users/me", method: "GET", rpm: 920, p50: 8, p95: 22, errorRate: 0.0 },
        { endpoint: "/api/search", method: "GET", rpm: 340, p50: 110, p95: 580, errorRate: 2.8 },
      ],
    },
  },
  {
    type: "topology",
    data: {
      nodes: [
        { id: "frontend", status: "ok", rpm: 500 },
        { id: "checkout", status: "danger", rpm: 200 },
      ],
      edges: [{ source: "frontend", target: "checkout", rpm: 200 }],
    },
  },
];

function App() {
  return (
    <div className="mx-auto max-w-3xl space-y-4 p-8">
      <h1 className="text-2xl font-bold">Block Renderer Test</h1>
      {testBlocks.map((block, i) => (
        <BlockRenderer key={i} block={block} />
      ))}
    </div>
  );
}

export default App;
