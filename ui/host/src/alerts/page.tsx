import { LoadingSkeleton } from "../loading-skeleton";
import { LiveDataMeta } from "../live-data-meta";
import { ConfirmDialog, DataState, Notice, PageHeader, StatusBadge } from "@labstack/ui";
import { ActionIcon, Button, Group, Menu, Modal, Stack, Switch, Table, Text, Title } from "@mantine/core";
import { DotsThree, Plus } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { exactTimestamp } from "../../../format";
import { useViewer } from "../auth";
import { useOpener } from "../opener";
import { alertsKey, createRule, deleteRule, listAlerts, listRules, replaceRule, rulesKey, testRule, type Rule, type RuleDraft, type RuleTest } from "./api";
import { RuleForm } from "./rule-form";
import { formatValue, variables } from "./values";

const stateTone: Record<string, "danger" | "warning" | "neutral"> = { firing: "danger", pending: "warning", resolved: "neutral" } as const;
const when = (value?: string) => value ? exactTimestamp(value) : "—";

/** The rollup variables a rule reads, as the FanQL source and measure that chart them. */
const charts: Record<string, string> = { error_rate: "spans | error_rate()", p95: "spans | p95(duration)", throughput: "spans | count()", log_count: "logs | count()" };

/** A FanQL time series of what the rule watches, named by the first variable it reads. */
function ruleQuery(rule: Pick<Rule, "service" | "expression">): string {
  const watched = variables(rule.expression).map(name => name.replace(/_delta$/, "")).find(name => name in charts) ?? "throughput";
  const [source, measure] = charts[watched].split(" | ");
  return rule.service ? `${source} where service:"${rule.service.replaceAll("\"", "\"\"")}" | ${measure} every auto` : `${source} | ${measure} by service every auto`;
}

/** Rules read per-service rollups with error_rate from 0 to 1; Explore reads raw telemetry and shows percent. */
const exploreNote = "Open in Explore charts raw telemetry, so values can differ slightly from a rule's rollups. Explore shows error rate as a percent: a rule's 0.05 is 5 there.";

/** Email outcomes that need attention on a rule; a sent email does not. */
const emailIssues: Record<string, { tone: "danger" | "warning"; label: string }> = {
  failed: { tone: "danger", label: "Email failed" },
  unavailable: { tone: "danger", label: "Email unavailable" },
  suppressed: { tone: "warning", label: "Email suppressed" },
  retrying: { tone: "warning", label: "Email retrying" },
};

/** Alert rules and their history (proposal 0004, section 4); works without chat. */
export function AlertsPage({ onExplore }: { onExplore(query: string): void }) {
  const client = useQueryClient();
  // Viewers read; operators and admins hold alerts:manage.
  const canManage = useViewer().role !== "viewer";
  const rules = useQuery({ queryKey: rulesKey, queryFn: ({ signal }) => listRules(signal), refetchInterval: 30_000 });
  const alerts = useQuery({ queryKey: alertsKey, queryFn: ({ signal }) => listAlerts(signal), refetchInterval: 30_000 });
  const [editing, setEditing] = useState<{ rule?: Rule }>();
  const [deleting, setDeleting] = useState<Rule>();
  const [tested, setTested] = useState<{ rule: Rule; result?: RuleTest; error?: string }>();
  // Dialogs return focus to what opened them: New rule, or the rule's actions menu.
  const opener = useOpener();
  const newRule = useRef<HTMLButtonElement>(null);
  const menus = useRef(new Map<string, HTMLButtonElement>());
  const openFrom = (rule: Rule) => opener.remember(menus.current.get(rule.id) ?? null);
  const closeForm = () => { setEditing(undefined); opener.restore(newRule.current); };
  const closeDelete = () => { setDeleting(undefined); opener.restore(newRule.current); };
  const refresh = () => { void client.invalidateQueries({ queryKey: rulesKey }); void client.invalidateQueries({ queryKey: alertsKey }); };
  const toggle = useMutation({ mutationFn: (rule: Rule) => replaceRule(rule.id, { ...draftFields(rule), enabled: !rule.enabled }), onSuccess: refresh });
  const remove = useMutation({ mutationFn: (rule: Rule) => deleteRule(rule.id), onSuccess: () => { testRun.current++; closeDelete(); refresh(); } });

  const save = async (draft: RuleDraft) => {
    if (editing?.rule) await replaceRule(editing.rule.id, draft); else await createRule(draft);
    closeForm(); refresh();
  };
  // Only the latest test may answer, and only while its window is open.
  const testRun = useRef(0);
  const closeTest = () => { testRun.current++; setTested(undefined); opener.restore(newRule.current); };
  const runTest = (rule: Rule) => {
    const run = ++testRun.current;
    setTested({ rule });
    const answer = (next: { result?: RuleTest; error?: string }) => { if (testRun.current === run) setTested({ rule, ...next }); };
    testRule(rule.id).then(result => answer({ result }), (failure: unknown) => answer({ error: failure instanceof Error ? failure.message : "The test failed" }));
  };
  const byId = new Map((rules.data ?? []).map(rule => [rule.id, rule]));
  const lastFired = (rule: Rule) => (alerts.data ?? []).filter(a => a.rule_id === rule.id && a.fired_at).map(a => a.fired_at!).sort().at(-1);
  const active = (rule: Rule) => (alerts.data ?? []).filter(a => a.rule_id === rule.id && (a.state === "firing" || a.state === "pending"));
  const history = [...(alerts.data ?? [])].sort((a, b) => (b.fired_at ?? b.created_at).localeCompare(a.fired_at ?? a.created_at));

  return <Stack gap="lg" px={{base:"md",sm:"xl"}} pt="lg" pb="xl" maw={1600} mx="auto" data-alerts>
    <PageHeader title="Alerts" description="Evaluate service rules and deliver notifications when they fire." meta={<LiveDataMeta updatedAt={alerts.dataUpdatedAt} />} actions={canManage && <Button ref={newRule} leftSection={<Plus size={14} />} onClick={event => { opener.remember(event.currentTarget); setEditing({}); }}>New rule</Button>} />

    <Stack gap="xs">
      <Title order={2} fz={18}>Rules</Title>
      {rules.data && rules.data.length > 0 && <Text size="xs" c="dimmed">{exploreNote}</Text>}
      {rules.isLoading ? <DataState state="loading" loadingLabel="Loading alert rules" skeleton={<LoadingSkeleton shape="table" columns={7} rows={4} />} /> : rules.error ? <DataState state="error" error={{ message: `Rules could not be loaded: ${rules.error.message}`, onRetry: () => void rules.refetch() }} />
        : rules.data?.length === 0 ? <DataState state="empty" empty={{title: "No rules yet", description: canManage ? "Create a rule to get notified when a service misbehaves." : "An operator or admin can create rules.", action: canManage && <Button onClick={event => { opener.remember(event.currentTarget); setEditing({}); }}>New rule</Button>}} />
        : <Table.ScrollContainer minWidth={760}><Table verticalSpacing="sm">
          <Table.Thead><Table.Tr><Table.Th>Rule</Table.Th><Table.Th>Service</Table.Th><Table.Th>Expression</Table.Th><Table.Th>State</Table.Th><Table.Th>Last fired</Table.Th><Table.Th>Enabled</Table.Th><Table.Th w={48}><Text size="xs" c="dimmed">Actions</Text></Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>{rules.data?.map(rule => {
            const now = active(rule);
            return <Table.Tr key={rule.id}>
              <Table.Td><Text size="sm" fw={500}>{rule.name}</Text>{rule.description && <Text size="xs" c="dimmed">{rule.description}</Text>}
                {rule.email_status && emailIssues[rule.email_status] && <Group gap={6} mt={4} role="status"><StatusBadge tone={emailIssues[rule.email_status].tone}>{emailIssues[rule.email_status].label}</StatusBadge>{rule.email_status_detail && <Text size="xs" c="dimmed">{rule.email_status_detail}</Text>}</Group>}</Table.Td>
              <Table.Td><Text size="sm">{rule.service || "All services"}</Text></Table.Td>
              <Table.Td><Text size="xs" ff="monospace">{rule.expression}</Text>
                <Button size="compact-xs" variant="subtle" px={0} onClick={() => onExplore(ruleQuery(rule))}>Open in Explore</Button></Table.Td>
              <Table.Td>{!rule.enabled ? <StatusBadge tone="neutral">Disabled</StatusBadge> : !alerts.isSuccess ? <StatusBadge tone="neutral">Unknown</StatusBadge>
                : now.length > 0 ? <Group gap={4}>{now.map(a => <StatusBadge key={a.id} tone={stateTone[a.state] ?? "neutral"}>{a.state.charAt(0).toUpperCase() + a.state.slice(1)} · {a.service}</StatusBadge>)}</Group> : <StatusBadge tone="success">Ok</StatusBadge>}</Table.Td>
              <Table.Td><Text size="sm" ff="monospace">{alerts.isSuccess ? when(lastFired(rule)) : "—"}</Text></Table.Td>
              <Table.Td><Switch aria-label={`Enable ${rule.name}`} checked={rule.enabled} disabled={!canManage || toggle.isPending} onChange={() => toggle.mutate(rule)} /></Table.Td>
              <Table.Td>{canManage && <Menu position="bottom-end"><Menu.Target><ActionIcon ref={node => { if (node) menus.current.set(rule.id, node); else menus.current.delete(rule.id); }} variant="subtle" color="gray" aria-label={`${rule.name} actions`}><DotsThree size={18} /></ActionIcon></Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item onClick={() => { openFrom(rule); setEditing({ rule }); }}>Edit</Menu.Item>
                  <Menu.Item onClick={() => { openFrom(rule); runTest(rule); }}>Test against current data</Menu.Item>
                  <Menu.Item color="bad" onClick={() => { openFrom(rule); setDeleting(rule); }}>Delete</Menu.Item>
                </Menu.Dropdown></Menu>}</Table.Td>
            </Table.Tr>;
          })}</Table.Tbody>
        </Table></Table.ScrollContainer>}
      {toggle.error && <Notice tone="danger">{toggle.error.message}</Notice>}
    </Stack>

    <Stack gap="xs">
      <Title order={2} fz={18}>History</Title>
      {alerts.isLoading ? <DataState state="loading" loadingLabel="Loading alert history" skeleton={<LoadingSkeleton shape="table" columns={7} rows={4} />} /> : alerts.error ? <Notice tone="danger">Alert history could not be loaded: {alerts.error.message}</Notice>
        : history.length === 0 ? <DataState state="empty" empty={{title: "Nothing has fired yet", description: "Alerts appear here when a rule fires."}} />
        : <Table.ScrollContainer minWidth={760} scrollAreaProps={{ viewportProps: { tabIndex: 0, role: "region", "aria-label": "Alert history" } }}><Table verticalSpacing="xs">
          <Table.Thead><Table.Tr><Table.Th>State</Table.Th><Table.Th>Rule</Table.Th><Table.Th>Service</Table.Th><Table.Th ta="right">Value</Table.Th><Table.Th>Fired</Table.Th><Table.Th>Resolved</Table.Th><Table.Th>Delivery</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>{history.map(a => <Table.Tr key={a.id}>
            <Table.Td><StatusBadge tone={stateTone[a.state] ?? "neutral"}>{a.state.charAt(0).toUpperCase() + a.state.slice(1)}</StatusBadge></Table.Td>
            <Table.Td><Text size="sm">{byId.get(a.rule_id)?.name ?? a.rule_id}</Text></Table.Td>
            <Table.Td><Text size="sm">{a.service}</Text></Table.Td>
            <Table.Td ta="right"><Text size="sm" ff="monospace">{a.value === undefined ? "—" : formatValue(variables(byId.get(a.rule_id)?.expression ?? "")[0], a.value)}</Text></Table.Td>
            <Table.Td><Text size="sm" ff="monospace">{when(a.fired_at)}</Text></Table.Td>
            <Table.Td><Text size="sm" ff="monospace">{when(a.resolved_at)}</Text></Table.Td>
            <Table.Td><Text size="xs" c="dimmed">{a.last_delivery_status ? `${a.last_delivery_status} · ${when(a.last_delivery_at)}` : "—"}</Text></Table.Td>
          </Table.Tr>)}</Table.Tbody>
        </Table></Table.ScrollContainer>}
    </Stack>

    {editing && <RuleForm rule={editing.rule} onClose={closeForm} onSave={save} />}
    {deleting && <ConfirmDialog opened onClose={closeDelete} title={`Delete ${deleting.name}?`} confirmLabel="Delete rule" tone="danger" pending={remove.isPending ? "confirm" : null} error={remove.error?.message} onConfirm={() => remove.mutate(deleting)}>
      The rule <Text span ff="monospace">{deleting.name}</Text> and its alert history are removed. This cannot be undone.
    </ConfirmDialog>}
    {tested && <Modal opened onClose={closeTest} closeButtonProps={{ "aria-label": "Close test" }} title={`Test · ${tested.rule.name}`}>
      {tested.error ? <Notice tone="danger">{tested.error}</Notice>
        : !tested.result ? <DataState state="loading" loadingLabel="Testing the rule" rows={3} />
        : !tested.result.results?.length ? <DataState state="empty" empty={{ title: "No services have data in the current window." }} />
        : <Stack gap="xs">{tested.result.results.map(r => <Stack key={r.service} gap={2}>
          <Group justify="space-between">
            <Text size="sm">{r.service}</Text>
            {r.error ? <Text size="xs" c="bad">{r.error}</Text>
              : r.no_data ? <StatusBadge tone="warning">No recent data</StatusBadge>
              : <StatusBadge tone={r.triggered ? "danger" : "success"}>{r.triggered ? "Would fire" : "Quiet"}</StatusBadge>}
          </Group>
          {r.no_data ? <Text size="xs" c="dimmed">This service has sent no telemetry recently. Check its name.</Text>
            : <Text size="xs" c="dimmed" ff="monospace">{variables(tested.rule.expression).filter(name => typeof r.env?.[name] === "number").map(name => `${name} ${formatValue(name, r.env![name] as number)}`).join(" · ")}</Text>}
        </Stack>)}</Stack>}
    </Modal>}
  </Stack>;
}

function draftFields(rule: Rule): RuleDraft {
  const { id: _id, created_at: _created, updated_at: _updated, email_status: _status, email_status_detail: _detail, email_status_at: _at, ...draft } = rule;
  return draft;
}
