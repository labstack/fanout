import { Button, Collapse, Group, Loader, Paper, Stack, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { useId, useState } from "react";
import { changeChips, type BuildReceipt } from "./dashboard-receipt";
import { EditChips } from "./dashboard-change-chips";

const stageNames = { schema: "Telemetry", context: "Context", draft: "Draft", validation: "Validation", preview: "Preview", save: "Save" };
const seconds = (ms: number) => `${(ms / 1000).toFixed(1)} s`;
export function DashboardReceiptView({ receipt, running = false, activity = "", awaitingAnswer = false }: { receipt: BuildReceipt; running?: boolean; activity?: string; awaitingAnswer?: boolean }) {
  const [expanded, setExpanded] = useState(false), detailsID = useId();
  const saved = receipt.saved, check = saved?.receipt.save_check;
  const completed = Object.values(receipt.stages).filter(s => s.state === "complete").length;
  const summary = [
    receipt.stages.schema.state === "complete" ? "Read telemetry" : undefined,
    receipt.panel_count !== undefined ? `${receipt.panel_count} ${receipt.panel_count === 1 ? "panel" : "panels"}` : undefined,
    check?.checked ? `Checked in ${seconds(check.elapsed_ms)}` : undefined,
    saved ? `Saved v${saved.version}` : undefined,
  ].filter(Boolean).join(" · ") || "Building dashboard";
  const chips = [
    ...(saved ? changeChips(saved.receipt) : []),
    ...receipt.corrections.map(c => `Fixed ${c.panel_id}.${c.path.replace(/^panels\[\d+\]\./, "")}`),
  ];
  return <Paper data-build-receipt={receipt.turn_id} data-dashboard-result={saved?.id} data-chat-anchor withBorder radius="md" p="sm">
    <Stack gap={6}>
      <Group gap="xs" justify="space-between">
        <Text size="sm" role="status" aria-live="polite" aria-atomic="true">{summary}<Text span c="dimmed" size="xs"> · {completed} stages complete</Text></Text>
        <Button component="button" variant="subtle" color="gray" size="compact-xs" aria-expanded={expanded} aria-controls={detailsID} onClick={() => setExpanded(v => !v)}>Details{chips.length > 4 ? ` (+${chips.length - 4})` : ""}</Button>
      </Group>
      {running && awaitingAnswer && <Group gap="xs" data-build-running><Loader type="dots" size="sm" /><Text c="dimmed" size="sm">{activity || "Analyzing your system"}</Text></Group>}
      <EditChips labels={chips.slice(0,4)} />
      {receipt.explanations.map(text => running ? text.split(" · ").filter(part => !part.endsWith(": incomplete")).join(" · ") : text).filter(Boolean).map((text,i) => <Text key={i} size="xs" c="dimmed" data-receipt-attention>{text}</Text>)}
      {saved && <Button renderRoot={(props) => <Link {...props} to="/dashboards/$dashboardId" params={{dashboardId:saved.id}} search={{}} />} variant="subtle" size="compact-sm" color="gray" style={{alignSelf:"flex-start"}}>Open dashboard</Button>}
      <Collapse expanded={expanded} id={detailsID}>
        <Stack gap={4}>
          {saved && <Text size="xs">{saved.label} {saved.name} · opens the current dashboard</Text>}
          {Object.entries(receipt.stages).map(([key,stage]) => <Text size="xs" c="dimmed" key={key}>{stageNames[key as keyof typeof stageNames]}: {running && stage.state === "incomplete" ? "in progress" : stage.state}{stage.elapsed_ms !== undefined ? ` · ${seconds(stage.elapsed_ms)}` : ""}</Text>)}
          {receipt.save_attempts.map((attempt,i) => attempt.state === "retried" && <Text size="xs" c="dimmed" key={attempt.call_id}>Save attempt {i+1}: retried</Text>)}
          {!!receipt.context_counts.deploys && <Text size="xs">{receipt.context_counts.deploys} deploys observed</Text>}
          {!!receipt.context_counts.anomalies && <Text size="xs">{receipt.context_counts.anomalies} anomalies observed</Text>}
          {receipt.panels.map(p => <Text size="xs" key={p.id}>Preview · {p.id}: {p.status}{p.elapsed_ms !== undefined ? ` · ${seconds(p.elapsed_ms)}` : ""}</Text>)}
          {check && <Text size="xs">Save check: {check.checked ? "checked" : "incomplete"} · {seconds(check.elapsed_ms)}</Text>}
          {check?.panels.map(p => <Text size="xs" key={p.id}>Save · {p.id}: {p.status} · {p.rows ?? 0} rows{p.elapsed_ms !== undefined ? ` · ${seconds(p.elapsed_ms)}` : ""}</Text>)}
          {chips.slice(4).map((text,i) => <Text size="xs" key={i}>{text}</Text>)}
          {receipt.corrections.map((c,i) => <Text size="xs" key={i}>Fixed {c.panel_id}.{c.path}: {c.message}</Text>)}
        </Stack>
      </Collapse>
    </Stack>
  </Paper>;
}
