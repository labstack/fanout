import { Markdown } from "@/components/markdown";
import type { TextBlockData } from "@/lib/types";

export function TextBlock({ data }: { data: TextBlockData }) {
  return (
    <div className="prose-themed">
      <Markdown>{data.content}</Markdown>
    </div>
  );
}
