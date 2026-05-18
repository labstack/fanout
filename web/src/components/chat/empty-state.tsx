import { useChatStore } from "@/stores/chat";
import { Radio, ArrowRight } from "lucide-react";

const suggestions = [
  "What's the health of my services?",
  "Show error trends for the last hour",
  "Find the slowest endpoints",
  "Compare service latency week over week",
];

export function EmptyState() {
  const sendMessage = useChatStore((s) => s.sendMessage);

  return (
    <div className="flex flex-col items-center justify-center h-full px-4 animate-fade-up">
      <div className="mb-10">
        <div className="flex items-center justify-center w-16 h-16 rounded-2xl bg-primary/15 border border-primary/25 mb-5 mx-auto shadow-[0_0_24px_-4px_rgba(96,165,250,0.15)]">
          <Radio className="h-8 w-8 text-primary" />
        </div>
        <h1 className="font-heading text-xl font-bold tracking-tight text-center text-foreground">
          Fanout
        </h1>
        <p className="text-sm text-muted-foreground text-center mt-1.5">
          Ask about your services, traces, logs, and metrics
        </p>
      </div>

      <div className="grid gap-2 w-full max-w-lg">
        {suggestions.map((s) => (
          <button
            key={s}
            onClick={() => sendMessage(s)}
            className="group flex items-center justify-between gap-3 px-4 py-2.5 text-[13px] text-left rounded-lg border border-border/60 bg-surface-1/80 hover:bg-surface-2 hover:border-border transition-colors duration-150"
          >
            <span className="text-muted-foreground group-hover:text-foreground transition-colors">
              {s}
            </span>
            <ArrowRight className="h-3.5 w-3.5 text-muted-foreground/30 group-hover:text-primary group-hover:translate-x-0.5 transition-[color,transform] shrink-0" />
          </button>
        ))}
      </div>
    </div>
  );
}
