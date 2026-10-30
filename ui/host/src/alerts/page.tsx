import { ActionIcon, Alert, Badge, Button, Group, Loader, Menu, Modal, Stack, Switch, Table, Text, Title } from "@mantine/core";
import { DotsThree, Plus } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { exactTimestamp } from "../../../format";
import { useViewer } from "../auth";
import { alertsKey, createRule, deleteRule, listAlerts, listRules, replaceRule, rulesKey, testRule, type Rule, type RuleDraft, type RuleTest } from "./api";
import { RuleForm } from "./rule-form";

const stateColor: Record<string, string> = { firing: "bad", pending: "warn", resolved: "gray" };
const when = (value?: string) => value ? exactTimestamp(value) : "—";

/** Alert rules and their history (proposal 0004, section 4); works without chat. */
export function AlertsPage() {
  const client = useQueryClient();
  // Viewers read; operators and admins hold alerts:manage.
  const canManage = useViewer().role !== "viewer";
  const rules = useQuery({ queryKey: rulesKey, queryFn: ({ signal }) => listRules(signal), refetchInterval: 30_000 });
  const alerts = useQuery({ queryKey: alertsKey, queryFn: ({ signal }) => listAlerts(signal), refetchInterval: 30_000 });
  const [editing, setEditing] = useState<{ rule?: Rule }>();
  const [deleting, setDeleting] = useState<Rule>();
  const [tested, setTested] = useState<{ rule: Rule; result?: RuleTest; error?: string }>();
  const refresh = () => { void client.invalidateQueries({ queryKey: rulesKey }); void client.invalidateQueries({ queryKey: alertsKey }); };
  const toggle = useMutation({ mutationFn: (rule: Rule) => replaceRule(rule.id, { ...draftFields(rule), enabled: !rule.enabled }), onSuccess: refresh });
  const remove = useMutation({ mutationFn: (rule: Rule) => deleteRule(rule.id), onSuccess: () => { setDeleting(undefined); refresh(); } });

  const save = async (draft: RuleDraft) => {
    if (editing?.rule) await replaceRule(editing.rule.id, draft); else await createRule(draft);
    setEditing(undefined); refresh();
  };
  const runTest = (rule: Rule) => {
    setTested({ rule });
    testRule(rule.id).then(result => setTested({ rule, result }), (failure: unknown) => setTested({ rule, error: failure instanceof Error ? failure.message : "The test failed" }));
  };
  const names = new Map((rules.data ?? []).map(rule => [rule.id, rule.name]));
  const active = (rule: Rule) => (alerts.data ?? []).filter(a => a.rule_id === rule.id && (a.state === "firing" || a.state === "pending"));
  const history = [...(alerts.data ?? [])].sort((a, b) => (b.fired_at ?? b.created_at).localeCompare(a.fired_at ?? a.created_at));

  return <Stack gap="lg" p="lg" maw={1400} data-alerts>
    <Group justify="space-between" align="flex-end">
      <Stack gap={2}>
        <Title order={2}>Alerts</Title>
        <Text size="sm" c="dimmed">Rules evaluate each service's rollups and deliver to a webhook when they fire.</Text>
      </Stack>
      {canManage && <Button leftSection={<Plus size={14} />} onClick={() => setEditing({})}>New rule</Button>}
    </Group>

    <Stack gap="xs">
      <Title order={3} fz={18}>Rules</Title>
      {rules.isLoading ? <Loader size="sm" /> : rules.error ? <Alert color="bad">Rules could not be loaded: {rules.error.message}</Alert>
        : rules.data?.length === 0 ? <Text size="sm" c="dimmed" data-alerts-empty>No rules yet.{canManage ? " Create one to get notified when a service misbehaves." : ""}</Text>
        : <Table.ScrollContainer minWidth={760}><Table verticalSpacing="sm" highlightOnHover>
          <Table.Thead><Table.Tr><Table.Th>Rule</Table.Th><Table.Th>Service</Table.Th><Table.Th>Expression</Table.Th><Table.Th>State</Table.Th><Table.Th>Enabled</Table.Th><Table.Th w={48} /></Table.Tr></Table.Thead>
          <Table.Tbody>{rules.data?.map(rule => {
            const now = active(rule);
            return <Table.Tr key={rule.id}>
              <Table.Td><Text size="sm" fw={500}>{rule.name}</Text>{rule.description && <Text size="xs" c="dimmed">{rule.description}</Text>}</Table.Td>
              <Table.Td><Text size="sm">{rule.service || "All services"}</Text></Table.Td>
              <Table.Td><Text size="xs" ff="monospace">{rule.expression}</Text></Table.Td>
              <Table.Td>{now.length > 0 ? <Group gap={4}>{now.map(a => <Badge key={a.id} color={stateColor[a.state]} variant="light">{a.state} · {a.service}</Badge>)}</Group> : <Text size="xs" c="dimmed">OK</Text>}</Table.Td>
              <Table.Td><Switch aria-label={`Enable ${rule.name}`} checked={rule.enabled} disabled={!canManage || toggle.isPending} onChange={() => toggle.mutate(rule)} /></Table.Td>
              <Table.Td>{canManage && <Menu position="bottom-end"><Menu.Target><ActionIcon variant="subtle" color="gray" aria-label={`${rule.name} actions`}><DotsThree size={18} /></ActionIcon></Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item onClick={() => setEditing({ rule })}>Edit</Menu.Item>
                  <Menu.Item onClick={() => runTest(rule)}>Test against current data</Menu.Item>
                  <Menu.Item color="bad" onClick={() => setDeleting(rule)}>Delete</Menu.Item>
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
            <Table.Td><Text size="sm">{names.get(a.rule_id) ?? a.rule_id}</Text></Table.Td>
            <Table.Td><Text size="sm">{a.service}</Text></Table.Td>
            <Table.Td><Text size="sm" ff="monospace">{a.value ?? "—"}</Text></Table.Td>
            <Table.Td><Text size="sm">{when(a.fired_at)}</Text></Table.Td>
            <Table.Td><Text size="sm">{when(a.resolved_at)}</Text></Table.Td>
            <Table.Td><Text size="xs" c="dimmed">{a.last_delivery_status ? `${a.last_delivery_status} · ${when(a.last_delivery_at)}` : "—"}</Text></Table.Td>
          </Table.Tr>)}</Table.Tbody>
        </Table></Table.ScrollContainer>}
    </Stack>

    {editing && <RuleForm rule={editing.rule} onClose={() => setEditing(undefined)} onSave={save} />}
    {deleting && <Modal opened onClose={() => { if (!remove.isPending) setDeleting(undefined); }} title={`Delete ${deleting.name}?`}>
      <Stack gap="md">
        <Text size="sm">The rule and its alert history are removed. This cannot be undone.</Text>
        {remove.error && <Alert color="bad">{remove.error.message}</Alert>}
        <Group justify="flex-end" gap="xs"><Button variant="default" disabled={remove.isPending} onClick={() => setDeleting(undefined)}>Cancel</Button><Button color="bad" loading={remove.isPending} onClick={() => remove.mutate(deleting)}>Delete rule</Button></Group>
      </Stack>
    </Modal>}
    {tested && <Modal opened onClose={() => setTested(undefined)} title={`Test · ${tested.rule.name}`}>
      {tested.error ? <Alert color="bad">{tested.error}</Alert>
        : !tested.result ? <Loader size="sm" aria-label="Testing the rule" />
        : !tested.result.results?.length ? <Text size="sm" c="dimmed">No services have data in the current window.</Text>
        : <Stack gap={4}>{tested.result.results.map(r => <Group key={r.service} justify="space-between">
          <Text size="sm">{r.service}</Text>
          {r.error ? <Text size="xs" c="bad">{r.error}</Text> : <Badge color={r.triggered ? "bad" : "ok"} variant="light">{r.triggered ? "Would fire" : "Quiet"}</Badge>}
        </Group>)}</Stack>}
    </Modal>}
  </Stack>;
}

function draftFields(rule: Rule): RuleDraft {
  const { id: _id, created_at: _created, updated_at: _updated, ...draft } = rule;
  return draft;
}
