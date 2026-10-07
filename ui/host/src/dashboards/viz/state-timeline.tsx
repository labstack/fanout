import { Group, Stack, Text } from "@mantine/core";
import { AnalysisChart, type AnalysisProps } from "./analysis-chart";

export function StateTimelineViz(props: AnalysisProps) {
  return <Stack gap={4} style={{ flex: "1 1 auto", minHeight: 0 }}>
    <AnalysisChart {...props} height={Math.max(60, props.height - 24)} />
    <Group gap="sm" wrap="nowrap" role="list" aria-label="State legend" style={{ flexShrink: 0 }}>
      {([ ["● OK", "ok"], ["■ Warn", "warn"], ["◆ Bad", "bad"], ["○ Unknown", "dimmed"] ] as const).map(([label, color]) => <Text key={label} size="xs" c={color} role="listitem">{label}</Text>)}
    </Group>
  </Stack>;
}
