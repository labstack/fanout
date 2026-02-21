import ReactMarkdown from "react-markdown";
import type { TextBlockData } from "@/lib/types";

export function TextBlock({ data }: { data: TextBlockData }) {
  return (
    <div className="prose prose-sm dark:prose-invert max-w-none">
      <ReactMarkdown>{data.content}</ReactMarkdown>
    </div>
  );
}
