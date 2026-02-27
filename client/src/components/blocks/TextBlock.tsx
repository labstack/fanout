import { Markdown } from "@/components/Markdown";
import type { TextBlockData } from "@/lib/types";

export function TextBlock({ data }: { data: TextBlockData }) {
  return (
    <div className="prose dark:prose-invert max-w-none">
      <Markdown>{data.content}</Markdown>
    </div>
  );
}
