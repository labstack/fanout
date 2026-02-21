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
