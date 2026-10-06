import { Text } from "@mantine/core";
import { AnalysisChart, type AnalysisProps } from "./analysis-chart";

export function StateTimelineViz(props: AnalysisProps) {
  return <><AnalysisChart {...props} /><Text size="xs" c="dimmed">Gaps and gray cells mean unknown.</Text></>;
}
