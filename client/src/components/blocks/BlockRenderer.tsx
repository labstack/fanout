import type { Block } from "@/lib/types";
import { TextBlock } from "./TextBlock";
import { GenericBlock } from "./GenericBlock";

export function BlockRenderer({ block }: { block: Block }) {
  switch (block.type) {
    case "text":
      return <TextBlock data={block.data as import("@/lib/types").TextBlockData} />;
    default:
      return <GenericBlock type={block.type} data={block.data} />;
  }
}
