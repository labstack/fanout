import { GraphChart } from "echarts/charts";
import { Stack, Text } from "@mantine/core";
import { useMemo } from "react";
import type { Topology } from "../../../contracts";
import { chartTheme, statusHex } from "../../../chart";
import { EChart, useECharts } from "../echart";
import { useObservability, widgetParams } from "./data";
import { Empty, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

useECharts([GraphChart]);

export default function TopologyWidget({ widget, filters, dark, onOpenChat }: WidgetBodyProps) {
  const params = widgetParams(filters, widget.config, ["service"]);
  const topology = useObservability<Topology>("topology", params);
  const data = topology.data?.data;
  const option = useMemo(() => {
    if (!data) return null;
    const colors = chartTheme(dark);
    const status = statusHex(dark);
    const healthHex = (health: string) => (health === "unhealthy" ? status.bad : health === "degraded" ? status.warn : status.ok);
    // A circular layout assigns ring positions by data index, and the server
    // orders services by error rate, so the ring would still turn over whenever
    // a spike aged out. Sorting by name makes a service's position a function
    // of the service set alone: the same map on every one of the 30 second
    // polls. The chat view keeps the force layout — it is drawn once, not
    // polled.
    const nodes = [...data.nodes].sort((a, b) => a.service.localeCompare(b.service));
    return {
      tooltip: { backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } },
      series: [{
        type: "graph", layout: "circular", circular: { rotateLabel: false }, roam: false, draggable: false,
        label: { show: true, position: "bottom", color: colors.text, fontSize: 10 },
        edgeSymbol: ["none", "arrow"], edgeSymbolSize: 6,
        data: nodes.map((node) => ({ id: node.service, name: node.service, value: node.spans, symbolSize: Math.min(34, 18 + Math.log10(Math.max(node.spans, 1)) * 4), itemStyle: { color: colors.surface, borderColor: healthHex(node.health), borderWidth: 3 } })),
        links: data.edges.map((edge) => ({ source: edge.caller, target: edge.callee, value: edge.calls, lineStyle: { width: Math.min(4, 1 + Math.log10(Math.max(edge.calls, 1))), color: edge.error_rate >= 0.05 ? status.bad : colors.muted, opacity: 0.5, curveness: 0.08 } })),
        emphasis: { focus: "adjacency" },
      }],
    };
  }, [dark, data]);
  if (topology.isError) return <WidgetError retry={() => void topology.refetch()} />;
  if (data && data.nodes.length === 0) return <Empty text="No service relationships in this window" />;
  return <Stack gap={4} h="100%">
    {option && <EChart option={option} height="100%" className="widget-chart" label="Service dependency graph" onClick={(params) => { const item = params as { dataType?: string; data?: { id?: string } }; if (item.dataType === "node" && item.data?.id) onOpenChat(`Investigate the ${item.data.id} service. Explain its errors and latency.`); }} />}
    {data && <Text c="dimmed" size="xs">{data.nodes.length} services · {data.edges.length} {data.edges.length === 1 ? "route" : "routes"}</Text>}
  </Stack>;
}
