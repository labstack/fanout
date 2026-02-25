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
      <div className="mb-8">
        <div className="flex items-center justify-center w-14 h-14 rounded-2xl bg-primary/10 border border-primary/20 mb-4 mx-auto">
          <Radio className="h-7 w-7 text-primary" />
        </div>
        <h1 className="font-mono text-lg font-bold tracking-widest text-center text-foreground/80 uppercase">
          Fanout
        </h1>
        <p className="text-sm text-muted-foreground text-center mt-1">
          Observability, conversational
        </p>
      </div>

      <div className="grid gap-2 w-full max-w-md">
        {suggestions.map((s) => (
          <button
            key={s}
            onClick={() => sendMessage(s)}
            className="group flex items-center justify-between gap-3 px-4 py-3 text-sm text-left rounded-xl border border-border/50 bg-card/50 hover:bg-card hover:border-border transition-all duration-200"
          >
            <span className="text-muted-foreground group-hover:text-foreground transition-colors">
              {s}
            </span>
            <ArrowRight className="h-3.5 w-3.5 text-muted-foreground/50 group-hover:text-primary group-hover:translate-x-0.5 transition-all shrink-0" />
          </button>
        ))}
      </div>
    </div>
  );
}
