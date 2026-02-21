import type { Block, TextBlockData, MetricsBlockData } from "@/lib/types";
import { TextBlock } from "./TextBlock";
import { MetricsBlock } from "./MetricsBlock";
import { GenericBlock } from "./GenericBlock";

export function BlockRenderer({ block }: { block: Block }) {
  switch (block.type) {
    case "text":
      return <TextBlock data={block.data as TextBlockData} />;
    case "metrics":
      return <MetricsBlock data={block.data as MetricsBlockData} />;
    default:
      return <GenericBlock type={block.type} data={block.data} />;
  }
}
