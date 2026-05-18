import type {
  Block,
  ComparisonData,
  EndpointsData,
  HeatmapBlockData,
  LogsBlockData,
  MetricsBlockData,
  SankeyData,
  TableBlockData,
  TimeseriesBlockData,
  TopologyData,
  TraceWaterfallData,
} from "@/lib/types";
import type { AIAction } from "@/components/ai/ai-action-bar";

function inferTableService(data: TableBlockData): string | null {
  for (const row of data.rows) {
    for (const [key, value] of Object.entries(row)) {
      if (typeof value !== "string") continue;
      if (key.toLowerCase().includes("service") && value.trim().length > 0) {
        return value.trim();
      }
    }
  }
  return null;
}

function riskiestTopologyService(data: TopologyData): string | null {
  if (data.nodes.length === 0) return null;
  const [worst] = [...data.nodes].sort((a, b) => {
    const left = a.errors * 100 + a.p95;
    const right = b.errors * 100 + b.p95;
    return right - left;
  });
  return worst?.id ?? null;
}

function riskiestEndpoint(data: EndpointsData): string | null {
  if (data.endpoints.length === 0) return null;
  const [worst] = [...data.endpoints].sort((a, b) => {
    const left = a.errorRate * 1000 + a.p95 + a.p99;
    const right = b.errorRate * 1000 + b.p95 + b.p99;
    return right - left;
  });
  return worst ? `${worst.method} ${worst.path}` : null;
}

function rootTraceService(data: TraceWaterfallData): string | null {
  if (data.spans.length === 0) return null;
  const [root] = [...data.spans].sort((a, b) => a.start - b.start);
  return root?.service ?? null;
}

function dominantLogService(data: LogsBlockData): string | null {
  const counts = new Map<string, number>();
  for (const entry of data.entries) {
    counts.set(entry.service, (counts.get(entry.service) ?? 0) + 1);
  }
  let best: string | null = null;
  let max = -1;
  for (const [service, count] of counts) {
    if (count > max) {
      best = service;
      max = count;
    }
  }
  return best;
}

function dominantSankeyNode(data: SankeyData): string | null {
  if (data.nodes.length === 0) return null;
  const [node] = [...data.nodes].sort((a, b) => b.rpm - a.rpm);
  return node?.label ?? node?.id ?? null;
}

function buildChartActions(
  title: string,
  promptNoun: string,
  comparePrompt: string,
): AIAction[] {
  return [
    {
      label: "Explain",
      prompt: `Explain the main signal shifts in ${title} and call out the most suspicious ${promptNoun}.`,
      kind: "explain",
    },
    {
      label: "Compare",
      prompt: comparePrompt,
      kind: "compare",
    },
    {
      label: "Create alert",
      prompt: `Propose a useful alert rule from ${title}, with thresholds and rationale.`,
      kind: "alert",
    },
  ];
}

export function getBlockActions(block: Block): AIAction[] {
  switch (block.type) {
    case "metrics": {
      const data = block.data as MetricsBlockData;
      const riskiest = data.items.find((item) => item.status !== "ok")?.label ?? data.items[0]?.label;
      return [
        {
          label: "Explain",
          prompt: "Summarize the key signals in this metrics snapshot and tell me what matters most.",
          kind: "explain",
        },
        {
          label: "Drill down",
          prompt: riskiest
            ? `Explain why ${riskiest} stands out in this metrics snapshot and what I should inspect next.`
            : "Tell me which metric in this snapshot deserves the next drill-down.",
          kind: "drill",
        },
        {
          label: "Create alert",
          prompt: "Create an alert idea from the riskiest metric in this snapshot, including threshold and why it matters.",
          kind: "alert",
        },
      ];
    }
    case "table": {
      const data = block.data as TableBlockData;
      const service = inferTableService(data);
      return [
        {
          label: "Explain",
          prompt: "Explain the outliers in this table and tell me what matters most.",
          kind: "explain",
        },
        {
          label: "Drill down",
          prompt: service ? `Diagnose ${service}` : "Which row in this table deserves investigation first, and why?",
          kind: "drill",
        },
        {
          label: "Create alert",
          prompt: "Propose an alert based on the worst row in this table.",
          kind: "alert",
        },
      ];
    }
    case "timeseries": {
      const data = block.data as TimeseriesBlockData;
      return buildChartActions(
        data.title,
        "time window",
        `Compare the most abnormal interval in ${data.title} to its baseline and explain what changed.`,
      );
    }
    case "bar":
      return buildChartActions(
        (block.data as { title: string }).title,
        "bar",
        `Compare the biggest and smallest bars in ${(block.data as { title: string }).title} and explain the gap.`,
      );
    case "heatmap": {
      const data = block.data as HeatmapBlockData;
      return buildChartActions(
        data.title,
        "heat hotspot",
        `Explain which hotspot in ${data.title} looks most abnormal and what I should drill into next.`,
      );
    }
    case "trace_waterfall": {
      const data = block.data as TraceWaterfallData;
      const service = rootTraceService(data);
      return [
        {
          label: "Explain trace",
          prompt: "Explain this trace waterfall and identify the critical path.",
          kind: "explain",
        },
        {
          label: "Find bottleneck",
          prompt: "What is the bottleneck in this trace, and what evidence supports it?",
          kind: "drill",
        },
        {
          label: "Diagnose service",
          prompt: service ? `Diagnose ${service}` : "Diagnose the service most responsible for this trace.",
          kind: "drill",
        },
      ];
    }
    case "topology": {
      const data = block.data as TopologyData;
      const service = riskiestTopologyService(data);
      return [
        {
          label: "Explain topology",
          prompt: "Explain this topology and call out the riskiest service and edge.",
          kind: "explain",
        },
        {
          label: "Find blast radius",
          prompt: "Which service in this topology has the highest blast radius, and why?",
          kind: "compare",
        },
        {
          label: "Diagnose service",
          prompt: service ? `Diagnose ${service}` : "Diagnose the riskiest service in this topology.",
          kind: "drill",
        },
      ];
    }
    case "flame_graph":
      return [
        {
          label: "Explain flame graph",
          prompt: "Explain the hottest frames in this flame graph and identify the dominant cost center.",
          kind: "explain",
        },
        {
          label: "Find bottleneck",
          prompt: "Which service or frame dominates this flame graph, and what should I optimize first?",
          kind: "drill",
        },
        {
          label: "Compare",
          prompt: "Tell me how I should compare this flame graph against a healthy baseline.",
          kind: "compare",
        },
      ];
    case "sankey": {
      const data = block.data as SankeyData;
      const node = dominantSankeyNode(data);
      return [
        {
          label: "Explain flow",
          prompt: "Explain the heaviest flow in this graph and call out the most suspicious handoff.",
          kind: "explain",
        },
        {
          label: "Find bottleneck",
          prompt: node
            ? `Explain why ${node} dominates this flow graph and what to investigate next.`
            : "Which node is the main bottleneck in this flow graph?",
          kind: "drill",
        },
        {
          label: "Create alert",
          prompt: "Propose an alert based on the riskiest path in this flow graph.",
          kind: "alert",
        },
      ];
    }
    case "dep_matrix":
      return [
        {
          label: "Explain matrix",
          prompt: "Explain the most dangerous service relationship in this dependency matrix.",
          kind: "explain",
        },
        {
          label: "Find risk pair",
          prompt: "Which service pair in this dependency matrix is driving the most risk?",
          kind: "compare",
        },
        {
          label: "Next drill-down",
          prompt: "What should I investigate next from this dependency matrix?",
          kind: "drill",
        },
      ];
    case "endpoints": {
      const data = block.data as EndpointsData;
      const endpoint = riskiestEndpoint(data);
      return [
        {
          label: "Explain endpoints",
          prompt: "Explain the riskiest endpoints in this table and what patterns stand out.",
          kind: "explain",
        },
        {
          label: "Investigate endpoint",
          prompt: endpoint ? `Investigate ${endpoint}` : "Which endpoint here deserves investigation first?",
          kind: "drill",
        },
        {
          label: "Create alert",
          prompt: "Propose an alert rule for the worst endpoint in this table.",
          kind: "alert",
        },
      ];
    }
    case "correlation":
      return [
        {
          label: "Explain correlation",
          prompt: "Explain what is actually correlated here and which signal is most likely causal.",
          kind: "explain",
        },
        {
          label: "Test hypothesis",
          prompt: "What hypothesis should I test next from this correlation view?",
          kind: "drill",
        },
        {
          label: "Compare",
          prompt: "Compare the strongest correlated panel against the rest and explain the difference.",
          kind: "compare",
        },
      ];
    case "logs": {
      const data = block.data as LogsBlockData;
      const service = dominantLogService(data);
      return [
        {
          label: "Cluster logs",
          prompt: "Cluster these logs by likely root cause and summarize the dominant pattern.",
          kind: "explain",
        },
        {
          label: "Find traces",
          prompt: "Show the traces behind the noisiest failures in this log block.",
          kind: "drill",
        },
        {
          label: "Diagnose service",
          prompt: service ? `Diagnose ${service}` : "Diagnose the service most associated with these logs.",
          kind: "drill",
        },
      ];
    }
    case "comparison": {
      const data = block.data as ComparisonData;
      return [
        {
          label: "Explain regression",
          prompt: `Explain the biggest regressions between ${data.leftLabel} and ${data.rightLabel}.`,
          kind: "explain",
        },
        {
          label: "Significance",
          prompt: `Which differences between ${data.leftLabel} and ${data.rightLabel} are statistically meaningful?`,
          kind: "compare",
        },
        {
          label: "Next drill-down",
          prompt: `What should I investigate next from this comparison between ${data.leftLabel} and ${data.rightLabel}?`,
          kind: "drill",
        },
      ];
    }
    case "text":
      return [
        {
          label: "Summarize",
          prompt: "Summarize the key point here in one sentence and tell me what to investigate next.",
          kind: "explain",
        },
      ];
    default:
      return [];
  }
}
