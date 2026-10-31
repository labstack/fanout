import { ActionIcon, Alert, Badge, Button, Group, Loader, Menu, Modal, Stack, Switch, Table, Text, Title } from "@mantine/core";
import { DotsThree, Plus } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { exactTimestamp } from "../../../format";
import { useViewer } from "../auth";
import { useOpener } from "../opener";
import { alertsKey, createRule, deleteRule, listAlerts, listRules, replaceRule, rulesKey, testRule, type Rule, type RuleDraft, type RuleTest } from "./api";
import { RuleForm } from "./rule-form";
import { formatValue, variables } from "./values";

const stateColor: Record<string, string> = { firing: "bad", pending: "warn", resolved: "gray" };
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

  return <Stack gap="lg" p="lg" maw={1400} data-alerts>
    <Group justify="space-between" align="flex-end">
      <Stack gap={2}>
        <Title order={2}>Alerts</Title>
        <Text size="sm" c="dimmed">Rules evaluate each service's rollups and deliver to a webhook when they fire.</Text>
      </Stack>
      {canManage && <Button ref={newRule} leftSection={<Plus size={14} />} onClick={event => { opener.remember(event.currentTarget); setEditing({}); }}>New rule</Button>}
    </Group>

    <Stack gap="xs">
      <Title order={3} fz={18}>Rules</Title>
      {rules.data && rules.data.length > 0 && <Text size="xs" c="dimmed">{exploreNote}</Text>}
      {rules.isLoading ? <Loader size="sm" /> : rules.error ? <Alert color="bad">Rules could not be loaded: {rules.error.message}</Alert>
        : rules.data?.length === 0 ? <Text size="sm" c="dimmed" data-alerts-empty>No rules yet.{canManage ? " Create one to get notified when a service misbehaves." : ""}</Text>
        : <Table.ScrollContainer minWidth={760}><Table verticalSpacing="sm" highlightOnHover>
          <Table.Thead><Table.Tr><Table.Th>Rule</Table.Th><Table.Th>Service</Table.Th><Table.Th>Expression</Table.Th><Table.Th>State</Table.Th><Table.Th>Last fired</Table.Th><Table.Th>Enabled</Table.Th><Table.Th w={48} /></Table.Tr></Table.Thead>
          <Table.Tbody>{rules.data?.map(rule => {
            const now = active(rule);
            return <Table.Tr key={rule.id}>
              <Table.Td><Text size="sm" fw={500}>{rule.name}</Text>{rule.description && <Text size="xs" c="dimmed">{rule.description}</Text>}</Table.Td>
              <Table.Td><Text size="sm">{rule.service || "All services"}</Text></Table.Td>
              <Table.Td><Text size="xs" ff="monospace">{rule.expression}</Text>
                <Button size="compact-xs" variant="subtle" px={0} onClick={() => onExplore(ruleQuery(rule))}>Open in Explore</Button></Table.Td>
              <Table.Td>{!rule.enabled ? <Text size="xs" c="dimmed">Disabled</Text> : !alerts.isSuccess ? <Text size="xs" c="dimmed">Unknown</Text>
                : now.length > 0 ? <Group gap={4}>{now.map(a => <Badge key={a.id} color={stateColor[a.state]} variant="light">{a.state} · {a.service}</Badge>)}</Group> : <Text size="xs" c="dimmed">OK</Text>}</Table.Td>
              <Table.Td><Text size="sm">{alerts.isSuccess ? when(lastFired(rule)) : "—"}</Text></Table.Td>
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
      {toggle.error && <Alert color="bad">{toggle.error.message}</Alert>}
    </Stack>

    <Stack gap="xs">
      <Title order={3} fz={18}>History</Title>
      {alerts.isLoading ? <Loader size="sm" /> : alerts.error ? <Alert color="bad">Alert history could not be loaded: {alerts.error.message}</Alert>
        : history.length === 0 ? <Text size="sm" c="dimmed">Nothing has fired yet.</Text>
        : <Table.ScrollContainer minWidth={760}><Table verticalSpacing="xs">
          <Table.Thead><Table.Tr><Table.Th>State</Table.Th><Table.Th>Rule</Table.Th><Table.Th>Service</Table.Th><Table.Th>Value</Table.Th><Table.Th>Fired</Table.Th><Table.Th>Resolved</Table.Th><Table.Th>Delivery</Table.Th></Table.Tr></Table.Thead>
          <Table.Tbody>{history.map(a => <Table.Tr key={a.id}>
            <Table.Td><Badge color={stateColor[a.state] ?? "gray"} variant="light">{a.state}</Badge></Table.Td>
            <Table.Td><Text size="sm">{byId.get(a.rule_id)?.name ?? a.rule_id}</Text></Table.Td>
            <Table.Td><Text size="sm">{a.service}</Text></Table.Td>
            <Table.Td><Text size="sm" ff="monospace">{a.value === undefined ? "—" : formatValue(variables(byId.get(a.rule_id)?.expression ?? "")[0], a.value)}</Text></Table.Td>
            <Table.Td><Text size="sm">{when(a.fired_at)}</Text></Table.Td>
            <Table.Td><Text size="sm">{when(a.resolved_at)}</Text></Table.Td>
            <Table.Td><Text size="xs" c="dimmed">{a.last_delivery_status ? `${a.last_delivery_status} · ${when(a.last_delivery_at)}` : "—"}</Text></Table.Td>
          </Table.Tr>)}</Table.Tbody>
        </Table></Table.ScrollContainer>}
    </Stack>

    {editing && <RuleForm rule={editing.rule} onClose={closeForm} onSave={save} />}
    {deleting && <Modal opened onClose={() => { if (!remove.isPending) closeDelete(); }} title={`Delete ${deleting.name}?`}>
      <Stack gap="md">
        <Text size="sm">The rule and its alert history are removed. This cannot be undone.</Text>
        {remove.error && <Alert color="bad">{remove.error.message}</Alert>}
        <Group justify="flex-end" gap="xs"><Button variant="default" disabled={remove.isPending} onClick={closeDelete}>Cancel</Button><Button color="bad" loading={remove.isPending} onClick={() => remove.mutate(deleting)}>Delete rule</Button></Group>
      </Stack>
    </Modal>}
    {tested && <Modal opened onClose={closeTest} closeButtonProps={{ "aria-label": "Close test" }} title={`Test · ${tested.rule.name}`}>
      {tested.error ? <Alert color="bad">{tested.error}</Alert>
        : !tested.result ? <Loader size="sm" aria-label="Testing the rule" />
        : !tested.result.results?.length ? <Text size="sm" c="dimmed">No services have data in the current window.</Text>
        : <Stack gap="xs">{tested.result.results.map(r => <Stack key={r.service} gap={2}>
          <Group justify="space-between">
            <Text size="sm">{r.service}</Text>
            {r.error ? <Text size="xs" c="bad">{r.error}</Text>
              : r.no_data ? <Badge color="warn" variant="light">No recent data</Badge>
              : <Badge color={r.triggered ? "bad" : "ok"} variant="light">{r.triggered ? "Would fire" : "Quiet"}</Badge>}
          </Group>
          {r.no_data ? <Text size="xs" c="dimmed">This service has sent no telemetry recently. Check its name.</Text>
            : <Text size="xs" c="dimmed" ff="monospace">{variables(tested.rule.expression).filter(name => typeof r.env?.[name] === "number").map(name => `${name} ${formatValue(name, r.env![name] as number)}`).join(" · ")}</Text>}
        </Stack>)}</Stack>}
    </Modal>}
  </Stack>;
}

function draftFields(rule: Rule): RuleDraft {
  const { id: _id, created_at: _created, updated_at: _updated, ...draft } = rule;
  return draft;
}
