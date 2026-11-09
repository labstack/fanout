import { LoadingSkeleton } from "./loading-skeleton";
import { formatTimestamp } from "../../panels/units";
import { ConfirmDialog, DataState, KeyValueList, Notice, PageHeader } from "@labstack/ui";
import { Anchor, Box, Button, Code, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { getJSON } from "./api";
import { useFanoutApp } from "./app-context";
import { authorizedFetch, useRuntimeStatus } from "./auth";
import { ConnectTelemetry } from "./connect-telemetry";
import { docs } from "../../links";

type OAuthConnection = {
  client_id: string;
  client_name: string;
  connected_at: string;
  refreshed_at: string;
  scope: string;
};

const agentsQueryKey = ["settings", "oauth", "connections"] as const;

function ConnectedAgents() {
  const queryClient = useQueryClient();
  const agents = useQuery({ queryKey: agentsQueryKey, queryFn: () => getJSON<{ connections: OAuthConnection[] }>("/api/auth/oauth/connections") });
  const [selected, setSelected] = useState<OAuthConnection | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const disconnect = useMutation({
    onMutate: async () => {
      setAnnouncement("");
      await queryClient.cancelQueries({ queryKey: agentsQueryKey });
    },
    mutationFn: async (agent: OAuthConnection) => {
      const response = await authorizedFetch(`/api/auth/oauth/connections/${encodeURIComponent(agent.client_id)}/revoke`, { method: "POST" });
      if (!response.ok) throw new Error("Fanout could not disconnect this agent. Try again.");
    },
    onSuccess: (_, agent) => {
      queryClient.setQueryData<{ connections: OAuthConnection[] }>(agentsQueryKey, (data) => data && ({ connections: data.connections.filter((connection) => connection.client_id !== agent.client_id) }));
      setSelected(null);
      setAnnouncement(`${agent.client_name} disconnected.`);
    },
  });

  return <Stack gap="sm">
    <Box>
      <Title order={2} fz={22} lts="-0.02em">Connected agents</Title>
      <Text c="dimmed" size="sm" mt={2}>Agents you have authorized to access Fanout through MCP.</Text>
    </Box>
    <Paper withBorder radius="md" p="lg">
      {!agents.data
        ? agents.isLoading
          ? <DataState state="loading" loadingLabel="Loading connected agents" skeleton={<LoadingSkeleton shape="agents" rows={3} />} />
          : <Stack gap="sm" align="flex-start">
              <Notice tone="danger">Fanout could not load your connected agents.</Notice>
              <Button size="compact-sm" variant="light" onClick={() => void agents.refetch()}>Try again</Button>
            </Stack>
        : agents.data.connections.length === 0
          ? <DataState state="empty" empty={{title: "No agents connected.", description: "Authorize an agent to access this workspace.", action: <Anchor href={docs.connect} target="_blank" rel="noopener noreferrer" underline="always">Connect an agent over MCP</Anchor>}} />
          : <Stack gap="md">
              {agents.data.connections.map((agent) => <Group key={agent.client_id} justify="space-between" align="center">
                <Box style={{ minWidth: 0, overflowWrap: "anywhere" }}>
                  <Text size="sm" fw={600}>{agent.client_name}</Text>
                  <KeyValueList items={[{ label: "Connected", value: <time style={{fontFamily: "var(--mantine-font-family-monospace)"}} dateTime={agent.connected_at}>{formatTimestamp(Date.parse(agent.connected_at))}</time> }]} />
                </Box>
                <Button variant="default" size="sm" onClick={() => { disconnect.reset(); setSelected(agent); }} disabled={disconnect.isPending}>Disconnect</Button>
              </Group>)}
            </Stack>}
    </Paper>
    <Text size="sm" c="dimmed" role="status" aria-live="polite">{announcement}</Text>
    <ConfirmDialog opened={selected !== null} onClose={() => { disconnect.reset(); setSelected(null); }} title={`Disconnect ${selected?.client_name ?? "agent"}?`} confirmLabel="Disconnect agent" tone="danger" pending={disconnect.isPending ? "confirm" : null} error={disconnect.isError ? "Fanout could not disconnect this agent. Try again." : undefined} onConfirm={() => selected && disconnect.mutate(selected)}>
      <Text span ff="monospace">{selected?.client_name}</Text> loses access immediately. Your other agents and browser session stay connected. You can authorize the agent again to reconnect.
    </ConfirmDialog>
  </Stack>;
}

const localAdministration = "Administrators can still change an email or display name, suspend or reactivate an account, and delete an account.";
const accountOwnershipNotice = {
  identity_provider_roles: { tone: "info", title: "Managed by your identity provider", reason: `Your identity provider creates accounts at first sign-in and assigns roles, so Fanout refuses both here. ${localAdministration}` },
  identity_provider_roles_no_provisioning: { tone: "warning", title: "New people cannot be added", reason: `Your identity provider assigns roles, so Fanout refuses creating accounts and changing roles here, and this workspace does not create accounts at sign-in. People without an account stay out until automatic provisioning with an allow-list or SCIM is configured. ${localAdministration}` },
  identity_provider_directory: { tone: "info", title: "Managed by your identity provider", reason: "Your identity provider owns accounts through SCIM. Fanout refuses creating or deleting an account and changing its role, status, or email here. Display names can still be edited." },
} as const;

/** Fanout has no screen that changes other users, so this is where a person
 *  learns why those changes are refused. The mode comes from the server, which
 *  enforces it; local administration shows nothing. */
function AccountOwnership() {
  const { account_ownership: ownership } = useRuntimeStatus();
  if (ownership === "local") return null;
  return <Stack gap="sm">
    <Title order={2} fz={22} lts="-0.02em">User accounts</Title>
    <Notice tone={accountOwnershipNotice[ownership].tone} title={accountOwnershipNotice[ownership].title}>
      {accountOwnershipNotice[ownership].reason} <Anchor href={docs.authentication} target="_blank" rel="noopener noreferrer" underline="always">Authentication</Anchor> covers what stays in Fanout.
    </Notice>
  </Stack>;
}

export default function Settings() {
  const { agentAvailable } = useFanoutApp();

  return <Box w="100%" px={{ base: "md", sm: "xl" }} pt="lg" pb="xl">
    <Stack gap="xl">
      <PageHeader title="Settings" description="Manage telemetry connections and authorized agents." />
      <ConnectTelemetry />
      {!agentAvailable && <Stack gap="sm">
        <Title order={2} fz={22} lts="-0.02em">Chat is disabled</Title>
        <Paper withBorder radius="md" p="lg">
          <Stack gap="sm">
            <Text size="sm">To enable chat on a self-hosted instance, set <Code>FANOUT_AI_PROVIDER</Code> to <Code>anthropic</Code> with <Code>ANTHROPIC_API_KEY</Code>, or <Code>openai</Code> with <Code>OPENAI_API_KEY</Code>, then restart Fanout.</Text>
            <Text size="sm">You can also <Anchor href={docs.connect} target="_blank" rel="noopener noreferrer" underline="always">connect an MCP client</Anchor> such as Claude, Codex, or ChatGPT to use your own AI.</Text>
          </Stack>
        </Paper>
      </Stack>}
      <AccountOwnership />
      <ConnectedAgents />
    </Stack>
  </Box>;
}

/** The ingest token page. Its URL, /settings/ingest, is stable and doubles as a
 *  sign-in return path, so a sign-in link can land on it directly. */
export function IngestSettings() {
  return <Box w="100%" px={{ base: "md", sm: "xl" }} pt="lg" pb="xl">
    <Stack gap="xl">
      <PageHeader title="Ingest tokens" description="Create and revoke the tokens Collectors use to send telemetry." />
      <ConnectTelemetry />
    </Stack>
  </Box>;
}
