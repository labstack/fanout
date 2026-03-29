import { Markdown } from "@/components/Markdown";
import type { TextBlockData } from "@/lib/types";

export function TextBlock({ data }: { data: TextBlockData }) {
  return (
    <div className="prose dark:prose-invert max-w-none prose-a:text-[#818cf8] prose-a:no-underline hover:prose-a:underline prose-code:font-mono prose-code:text-[#c4b5fd] prose-code:bg-[#818cf8]/10 prose-code:px-1.5 prose-code:py-0.5 prose-code:rounded-md prose-code:text-sm prose-code:before:content-none prose-code:after:content-none prose-pre:bg-[#111113] prose-pre:border prose-pre:border-[#818cf8]/15 prose-pre:rounded-[14px] prose-blockquote:border-l-[#818cf8] prose-blockquote:border-l-2">
      <Markdown>{data.content}</Markdown>
    </div>
  );
}
