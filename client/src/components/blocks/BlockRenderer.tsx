import type { Block, TextBlockData, MetricsBlockData, TableBlockData } from "@/lib/types";
import { TextBlock } from "./TextBlock";
import { MetricsBlock } from "./MetricsBlock";
import { TableBlock } from "./TableBlock";
import { GenericBlock } from "./GenericBlock";

export function BlockRenderer({ block }: { block: Block }) {
  switch (block.type) {
    case "text":
      return <TextBlock data={block.data as TextBlockData} />;
    case "metrics":
      return <MetricsBlock data={block.data as MetricsBlockData} />;
    case "table":
      return <TableBlock data={block.data as TableBlockData} />;
    default:
      return <GenericBlock type={block.type} data={block.data} />;
  }
}
