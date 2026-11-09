import { LoadingSkeleton } from "./loading-skeleton";
import { formatTimestamp } from "../../panels/units";
import { ConfirmDialog, DataState, KeyValueList, Notice } from "@labstack/ui";
import { Anchor, Box, Button, Code, CopyButton, Group, Paper, Stack, Text, TextInput, Title, Tooltip } from "@mantine/core";
import { Check, Copy, Plus } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FormEvent, useState } from "react";
import { getJSON } from "./api";
import { authorizedFetch, useViewer } from "./auth";
import { docs } from "../../links";

type IngestSettings = {
  token_required: boolean;
  suggested_endpoint: string;
  tls_configured: boolean;
  header_name: string;
};

type IngestToken = {
  id: string;
  name: string;
  hint: string;
  created_at: string;
  last_used_at: string | null;
};

type IngestTokenList = { tokens: IngestToken[]; max_active: number };
type CreatedIngestToken = IngestSettings & { token: IngestToken; ingest_token: string };

const ingestQueryKey = ["settings", "ingest"] as const;
const ingestTokensQueryKey = ["settings", "ingest", "tokens"] as const;

/** Whether the exporter reaches this instance over TLS.
 *
 *  Three things can say so: the process holds the certificate itself, the
 *  advertised endpoint carries an https scheme, or it names port 443 — an
 *  operator pointing at a TLS-terminating proxy usually writes the port and
 *  not the scheme, and telling that collector to send plaintext produces a
 *  gRPC error that explains nothing. Anything else is treated as plaintext,
 *  which is what an unproxied instance serves. */
function overTLS(endpoint: string, tlsConfigured: boolean) {
  const value = endpoint.trim();
  return tlsConfigured || /^https:\/\//i.test(value) || /:443$/.test(value);
}

/** The collector block an engineer actually pastes, with this instance's own
 *  endpoint already in it. Everything else on the page is a fact about the
 *  connection; this is the thing that makes telemetry arrive.
 *
 *  The endpoint is quoted because an advertised IPv6 address arrives as
 *  "[2001:db8::1]:7520", which unquoted YAML reads as a flow sequence. The
 *  token is referenced as ${env:INGEST_TOKEN}, the name and the form the
 *  documentation and the README both use — the braced form alone is ambiguous
 *  where a distribution sets its own default config scheme. */
function collectorConfig(endpoint: string, header: string, tlsConfigured: boolean) {
  // Collector's gRPC exporter requires an explicit port even with an HTTP(S)
  // scheme. Keep the scheme so its TLS semantics survive normalization.
  if (/^https?:\/\//i.test(endpoint.trim())) {
    try {
      const url = new URL(endpoint);
      endpoint = `${url.protocol}//${url.hostname}:${url.port || (url.protocol === "https:" ? "443" : "80")}`;
    } catch {
      // Leave an invalid advertised override visible without breaking the page.
    }
  }
  const insecure = overTLS(endpoint, tlsConfigured) ? "" : "\n    tls:\n      insecure: true";
  return `exporters:
  otlp/fanout:
    endpoint: "${endpoint}"
    headers:
      ${header}: "Bearer \${env:INGEST_TOKEN}"${insecure}

service:
  pipelines:
    traces:
      exporters: [otlp/fanout]
    logs:
      exporters: [otlp/fanout]
    metrics:
      exporters: [otlp/fanout]`;
}

function Field({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return <KeyValueList layout="stacked" items={[{ label, hint, value: <Group gap="xs" wrap="nowrap" align="center">
      <Code style={{ flex: 1, overflowX: "auto" }}>{value}</Code>
      <CopyButton value={value} timeout={1500}>
        {({ copied, copy }) => <Tooltip label={copied ? "Copied" : `Copy ${label.toLowerCase()}`} withArrow>
          <Button variant="subtle" color="gray" size="sm" onClick={copy} aria-label={`Copy ${label.toLowerCase()}`}>
            {copied ? <Check size={15} weight="bold" /> : <Copy size={15} />}
          </Button>
        </Tooltip>}
      </CopyButton>
    </Group> }]} />;
}

function hintLabel(token: IngestToken) {
  return token.hint ? `foi_…${token.hint}` : "Not recorded";
}

function lastUsedLabel(token: IngestToken) {
  return token.last_used_at ? formatTimestamp(Date.parse(token.last_used_at)) : "Never";
}

/** The one place a token's value appears. The state holding it lives in this
 *  page and dies with it; the mutation that produced it keeps nothing. */
function CreatedToken({ created, config, onDone }: { created: { name: string; value: string }; config: string; onDone: () => void }) {
  const snippet = `INGEST_TOKEN=${created.value}\n\n${config}`;
  return <Paper withBorder radius="md" p="lg">
    <Stack gap="sm">
      <Notice tone="warning" title="Save this token now">Fanout shows it only here and stores only a digest. Close this page or choose Done and the value is gone; creating another token is the only way back.</Notice>
      <Text size="sm" fw={600}>{created.name}</Text>
      <Field label="Token" value={created.value} />
      <Stack gap={4}>
        <Text size="sm" fw={600}>Collector configuration</Text>
        <Text c="dimmed" size="xs">Set INGEST_TOKEN in the Collector&apos;s environment, not in a file you commit.</Text>
        <Code block style={{ fontSize: 12 }}>{snippet}</Code>
      </Stack>
      <Group>
        <CopyButton value={snippet} timeout={1500}>
          {({ copied, copy }) => <Button variant="light" size="compact-sm" leftSection={copied ? <Check size={15} weight="bold" /> : <Copy size={15} />} onClick={copy}>{copied ? "Copied" : "Copy configuration"}</Button>}
        </CopyButton>
        <Button variant="subtle" color="gray" size="compact-sm" onClick={onDone}>Done</Button>
      </Group>
    </Stack>
  </Paper>;
}

function TokenRow({ token, onRevoke, disabled }: { token: IngestToken; onRevoke: () => void; disabled: boolean }) {
  return <Group justify="space-between" align="center">
    <Box style={{ minWidth: 0, overflowWrap: "anywhere" }}>
      <Text size="sm" fw={600}>{token.name}</Text>
      <KeyValueList items={[
        { label: "Hint", value: <Text span ff="monospace" size="sm">{hintLabel(token)}</Text> },
        { label: "Created", value: <time style={{ fontFamily: "var(--mantine-font-family-monospace)" }} dateTime={token.created_at}>{formatTimestamp(Date.parse(token.created_at))}</time> },
        { label: "Last used", value: <Text span ff="monospace" size="sm">{lastUsedLabel(token)}</Text> },
      ]} />
    </Box>
    <Button variant="default" size="sm" onClick={onRevoke} disabled={disabled}>Revoke</Button>
  </Group>;
}

function IngestTokens({ hasToken, config }: { hasToken: boolean; config: string }) {
  const queryClient = useQueryClient();
  const tokens = useQuery({ queryKey: ingestTokensQueryKey, queryFn: () => getJSON<IngestTokenList>("/api/settings/ingest/tokens") });
  const [name, setName] = useState("");
  const [created, setCreated] = useState<{ name: string; value: string } | null>(null);
  const [revoking, setRevoking] = useState<IngestToken | null>(null);

  const refreshAfterChange = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: ingestTokensQueryKey, exact: true }),
    queryClient.invalidateQueries({ queryKey: ingestQueryKey, exact: true }),
  ]);

  const create = useMutation({
    // The plaintext is in this result, and a result otherwise stays cached for
    // minutes after its last observer leaves.
    gcTime: 0,
    // A list request already in flight would otherwise land after the create
    // and drop the new row.
    onMutate: () => queryClient.cancelQueries({ queryKey: ingestTokensQueryKey, exact: true }),
    mutationFn: async (tokenName: string) => {
      const response = await authorizedFetch("/api/settings/ingest/tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: tokenName }),
      }).catch(() => { throw new Error("Fanout could not be reached. A token may still have been created; reload this page before trying again."); });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({})) as { message?: string };
        throw new Error(payload.message ?? "Fanout could not create the token. Try again.");
      }
      return response.json() as Promise<CreatedIngestToken>;
    },
    onSuccess: (data) => {
      setCreated({ name: data.token.name, value: data.ingest_token });
      setName("");
      void refreshAfterChange();
    },
  });

  const revoke = useMutation({
    onMutate: () => queryClient.cancelQueries({ queryKey: ingestTokensQueryKey, exact: true }),
    mutationFn: async (token: IngestToken) => {
      const response = await authorizedFetch(`/api/settings/ingest/tokens/${encodeURIComponent(token.id)}/revoke`, { method: "POST" })
        .catch(() => { throw new Error("Fanout could not be reached. Reload this page to see whether the token was revoked."); });
      if (response.status === 404) throw new Error("That token was already revoked. Reload this page.");
      if (!response.ok) throw new Error("Fanout could not revoke the token. Try again.");
    },
    onSuccess: () => {
      setRevoking(null);
      void refreshAfterChange();
    },
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    if (name.trim() === "" || create.isPending) return;
    create.mutate(name.trim());
  }

  function dismissCreated() {
    setCreated(null);
    // Drops the mutation's result, and with gcTime: 0 the cache entry goes too.
    create.reset();
  }

  const active = tokens.data?.tokens ?? [];
  const lastToken = active.length === 1;
  const limit = tokens.data?.max_active ?? 0;
  const atLimit = tokens.data !== undefined && active.length >= limit;

  return <>
    {created && <CreatedToken created={created} config={config} onDone={dismissCreated} />}
    <Paper withBorder radius="md" p="lg">
      <Stack gap="md">
        <form onSubmit={submit}>
          <Stack gap="xs">
            <Group align="flex-end" wrap="nowrap">
              <TextInput label="Token name" description="Name the Collector or environment that will use it." placeholder="production-collector" maxLength={64} value={name} onChange={(event) => { create.reset(); setName(event.currentTarget.value); }} style={{ flex: 1 }} />
              <Button type="submit" leftSection={<Plus size={15} weight="bold" />} loading={create.isPending} disabled={name.trim() === "" || atLimit}>Create token</Button>
            </Group>
            {create.isError && <Notice tone="danger">{create.error.message}</Notice>}
            {atLimit && <Text c="dimmed" size="xs">This workspace has {limit} active tokens, the maximum. Revoke one to create another.</Text>}
          </Stack>
        </form>
        {!tokens.data
          ? tokens.isLoading
            ? <DataState state="loading" loadingLabel="Loading ingest tokens" skeleton={<LoadingSkeleton shape="agents" rows={3} />} />
            : <Stack gap="sm" align="flex-start">
                <Notice tone="danger">Fanout could not load the ingest tokens.</Notice>
                <Button size="compact-sm" variant="light" onClick={() => void tokens.refetch()}>Try again</Button>
              </Stack>
          : active.length === 0
            ? <Notice tone={hasToken ? "info" : "warning"} title="No active tokens">Ingest rejects every export until you create a token.</Notice>
            : <Stack gap="md">
                {active.map((token) => <TokenRow key={token.id} token={token} disabled={revoke.isPending} onRevoke={() => { revoke.reset(); setRevoking(token); }} />)}
              </Stack>}
      </Stack>
    </Paper>
    <ConfirmDialog opened={revoking !== null} onClose={() => { revoke.reset(); setRevoking(null); }} title={`Revoke ${revoking?.name ?? "token"}?`} confirmLabel="Revoke token" tone="danger" pending={revoke.isPending ? "confirm" : null} error={revoke.error?.message} onConfirm={() => revoking && revoke.mutate(revoking)}>
      {lastToken
        ? <Stack gap="xs"><Notice tone="danger" title="This is the last active token">Revoking it stops all ingest. Every Collector is rejected until you create a new token.</Notice><Text size="sm">The revocation takes effect immediately and cannot be undone.</Text></Stack>
        : "Any Collector using this token is rejected immediately. Other tokens keep working. This cannot be undone."}
    </ConfirmDialog>
  </>;
}

export function ConnectTelemetry() {
  const viewer = useViewer();
  const settings = useQuery({ queryKey: ingestQueryKey, queryFn: () => getJSON<IngestSettings>("/api/settings/ingest") });
  const [endpointOverride, setEndpointOverride] = useState<string | null>(null);

  // A page that has loaded stays loaded: one failed background refetch is not
  // a reason to replace a working screen with an alert.
  if (!settings.data) {
    return settings.isLoading
      ? <DataState state="loading" loadingLabel="Loading connection details" skeleton={<LoadingSkeleton shape="settings" />} />
      : <DataState state="error" error={{ message: "Fanout could not load this workspace's connection details.", onRetry: () => void settings.refetch() }} />;
  }

  const { suggested_endpoint: suggestedEndpoint, header_name: header, tls_configured: tlsConfigured, token_required: hasToken } = settings.data;
  const endpointInput = endpointOverride ?? suggestedEndpoint;
  const endpoint = endpointInput.trim() || suggestedEndpoint;
  const canManage = viewer.role === "admin";
  const authorization = `${header}: Bearer <your token>`;
  const config = collectorConfig(endpoint, header, tlsConfigured);

  return <Stack gap="xl">
    <Title order={2} fz={18}>Connect telemetry</Title>
    <Text c="dimmed" size="sm">Point an OpenTelemetry collector or SDK at this workspace.</Text>

    <Paper withBorder radius="md" p="lg">
      <Stack gap="lg">
        <Stack gap={4}>
          <Text size="sm" fw={600}>Endpoint</Text>
          <Group gap="xs" wrap="nowrap" align="center">
            <TextInput aria-label="Endpoint" value={endpointInput} onChange={(event) => setEndpointOverride(event.currentTarget.value)} style={{ flex: 1 }} styles={{ input: { fontFamily: "var(--mantine-font-family-monospace)" } }} />
            <CopyButton value={endpoint} timeout={1500}>
              {({ copied, copy }) => <Tooltip label={copied ? "Copied" : "Copy endpoint"} withArrow>
                <Button variant="subtle" color="gray" size="sm" onClick={copy} aria-label="Copy endpoint">
                  {copied ? <Check size={15} weight="bold" /> : <Copy size={15} />}
                </Button>
              </Tooltip>}
            </CopyButton>
          </Group>
          <Text c="dimmed" size="xs">{overTLS(endpoint, tlsConfigured) ? "OTLP over gRPC, secured with TLS." : "OTLP over gRPC. Fanout serves this endpoint in plaintext, so an exporter needs tls.insecure unless a proxy in front of it terminates TLS."} Edit this when collectors reach Fanout through another hostname.</Text>
        </Stack>
        <Field label="Authorization" value={authorization} hint="Send one of your workspace's ingest tokens. Fanout stores only a digest of each, so it cannot show you an existing one." />
      </Stack>
    </Paper>

    <Stack gap="sm">
      <Box>
        <Title order={2} fz={22} lts="-0.02em">Collector configuration</Title>
        <Text c="dimmed" size="sm" mt={2}>Merge these blocks into your collector configuration, alongside the receivers it already has, and set INGEST_TOKEN in its environment.</Text>
      </Box>
      <Paper withBorder radius="md" p="md">
        <Stack gap="sm">
          <Code block style={{ fontSize: 12 }}>{config}</Code>
          <Group justify="flex-end">
            <CopyButton value={config} timeout={1500}>
              {({ copied, copy }) => <Button variant="light" size="compact-sm" leftSection={copied ? <Check size={15} weight="bold" /> : <Copy size={15} />} onClick={copy}>
                {copied ? "Copied" : "Copy configuration"}
              </Button>}
            </CopyButton>
          </Group>
        </Stack>
      </Paper>
    </Stack>

    <Stack gap="sm">
      <Box>
        <Title order={2} fz={22} lts="-0.02em">Ingest tokens</Title>
        <Text c="dimmed" size="sm" mt={2}>
          {hasToken
            ? "Each Collector can have its own token. To rotate one, create a replacement, move the Collector across, then revoke the old token."
            : "This workspace has no active token, so it is rejecting telemetry."}
        </Text>
      </Box>
      {canManage
        ? <IngestTokens hasToken={hasToken} config={config} />
        : <Paper withBorder radius="md" p="lg"><Text size="sm" c="dimmed">An administrator creates and revokes ingest tokens.</Text></Paper>}
    </Stack>
    <Text c="dimmed" size="xs">
      <Anchor href={docs.ingest} target="_blank" rel="noopener noreferrer" underline="always">Sending telemetry</Anchor> covers SDK setup, the collector, and the OTLP/HTTP endpoint.
    </Text>
  </Stack>;
}
