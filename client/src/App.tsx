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
