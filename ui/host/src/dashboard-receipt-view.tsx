import { DataState, StatusBadge } from "@labstack/ui";
import { Button, Collapse, Group, Paper, Stack, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { useId, useState } from "react";
import { changeChips, stageNames, type BuildReceipt } from "./dashboard-receipt";
import { duration, plural } from "../../format";
import { DashboardChangeChips } from "./dashboard-change-chips";

const executed = (status: string) => status !== "invalid" && status !== "not_run";
export function DashboardReceiptView({ receipt, running = false, activity = "", awaitingAnswer = false, initialExpanded = false }: { receipt: BuildReceipt; running?: boolean; activity?: string; awaitingAnswer?: boolean; initialExpanded?: boolean }) {
  const [expanded, setExpanded] = useState(initialExpanded), detailsID = useId();
  const saved = receipt.saved, check = saved?.receipt.save_check;
  const panelTitle = (id:string) => Object.hasOwn(receipt.panel_titles,id) ? receipt.panel_titles[id] : id;
  const invalid = (check?.panels ?? receipt.panels).filter(p => p.status === "invalid").length;
  const attention = [...new Set([
    ...receipt.explanations.filter(text => !receipt.problems.some(p => text.startsWith(`${panelTitle(p.panel_id)}:`))),
    ...receipt.problems.map(p => `${panelTitle(p.panel_id)}: ${p.message}`),
  ])];
  const completed = Object.values(receipt.stages).filter(s => s.state === "complete").length;
  const summary = [
    receipt.stages.schema.state === "complete" ? "Read telemetry" : undefined,
    receipt.panel_count !== undefined ? plural(receipt.panel_count, "panel") : undefined,
    check?.checked ? `Checked in ${duration(check.elapsed_ms)}` : undefined,
    saved ? `Saved v${saved.version}` : undefined,
  ].filter(Boolean).join(" · ") || "Building dashboard";
  const chips = [
    ...(saved ? changeChips(saved.receipt) : []),
    ...[...new Map(receipt.corrections.map(c => [c.panel_id, `Fixed ${c.title}`])).values()],
  ];
  return <Paper data-build-receipt={receipt.turn_id} data-dashboard-result={saved?.id} data-chat-anchor withBorder radius="md" p="sm">
    <Stack gap={6}>
      <Group gap="xs" justify="space-between">
        <Text component="div" size="sm" role="status" aria-live="polite" aria-atomic="true">{summary}{invalid > 0 && <> · <StatusBadge tone="danger">{plural(invalid, "invalid panel")}</StatusBadge></>}<Text span c="dimmed" size="xs"> · {plural(completed, "stage")} complete</Text></Text>
        <Button component="button" variant="subtle" color="gray" size="compact-xs" aria-expanded={expanded} aria-controls={detailsID} onClick={() => setExpanded(v => !v)}>Details{chips.length > 4 ? ` (+${chips.length - 4})` : ""}</Button>
      </Group>
      {running && awaitingAnswer && <div data-build-running><DataState state="loading" loadingLabel={activity || "Analyzing your system"} rows={0} /></div>}
      <DashboardChangeChips labels={chips.slice(0,4)} />
      {attention.map(text => running ? text.split(" · ").filter(part => !part.endsWith(": incomplete")).join(" · ") : text).filter(Boolean).map((text,i) => <Text key={i} size="xs" c="dimmed" data-receipt-attention>{text}</Text>)}
      {saved && <Button renderRoot={(props) => <Link {...props} to="/dashboards/$dashboardId" params={{dashboardId:saved.id}} search={{}} />} variant="subtle" size="compact-sm" color="gray" style={{alignSelf:"flex-start"}}>Open dashboard</Button>}
      <Collapse expanded={expanded} id={detailsID}>
        <Stack gap={4}>
          {saved && <Text size="xs">{saved.label} {saved.name} · opens the current dashboard</Text>}
          <Text size="xs" fw={600}>Build stages</Text>
          {Object.entries(receipt.stages).map(([key,stage]) => <Text component="div" size="xs" key={key}><StatusBadge tone={stage.state === "failed" ? "danger" : stage.state === "complete" ? "success" : stage.state === "interrupted" ? "warning" : "neutral"}>{stageNames[key as keyof typeof stageNames]}: {running && stage.state === "incomplete" ? "in progress" : stage.state}</StatusBadge>{stage.elapsed_ms !== undefined && (stage.state === "complete" || key === "preview" && receipt.panels.some(p => executed(p.status))) ? ` · ${duration(stage.elapsed_ms)}` : ""}</Text>)}
          {receipt.save_attempts.map((attempt,i) => attempt.state === "retried" && <Text size="xs" c="dimmed" key={attempt.call_id}>Save attempt {i+1}: retried</Text>)}
          {!!receipt.context_counts.deploys && <Text size="xs">{receipt.context_counts.deploys} deploys observed</Text>}
          {!!receipt.context_counts.anomalies && <Text size="xs">{receipt.context_counts.anomalies} anomalies observed</Text>}
          <Text size="xs" fw={600}>Panel preview</Text>
          {receipt.panels.map(p => <Text component="div" size="xs" key={p.id}>Preview · {panelTitle(p.id)}: <StatusBadge tone={p.status === "ok" ? "success" : p.status === "empty" || p.status === "not_run" ? "neutral" : "danger"}>{p.status === "ok" ? "Ok" : p.status.charAt(0).toUpperCase() + p.status.slice(1)}</StatusBadge>{executed(p.status) && p.elapsed_ms !== undefined ? ` · ${duration(p.elapsed_ms)}` : ""}</Text>)}
          {check && <Text size="xs" fw={600}>Save verification</Text>}
          {check && <Text size="xs">Save check: {check.checked ? "checked" : "incomplete"}{check.checked ? ` · ${duration(check.elapsed_ms)}` : ""}</Text>}
          {check?.panels.map(p => <Text component="div" size="xs" key={p.id}>Save · {panelTitle(p.id)}: <StatusBadge tone={p.status === "ok" ? "success" : p.status === "empty" || p.status === "not_run" ? "neutral" : "danger"}>{p.status === "ok" ? "Ok" : p.status.charAt(0).toUpperCase() + p.status.slice(1)}</StatusBadge>{executed(p.status) && ` · ${plural(p.rows ?? 0, "row")}`}{executed(p.status) && p.elapsed_ms !== undefined ? ` · ${duration(p.elapsed_ms)}` : ""}</Text>)}
          {chips.slice(4).map((text,i) => <Text size="xs" key={i}>{text}</Text>)}
          {receipt.corrections.map((c,i) => <Text size="xs" key={i}>Fixed {c.title}: {c.message}</Text>)}
        </Stack>
      </Collapse>
    </Stack>
  </Paper>;
}
