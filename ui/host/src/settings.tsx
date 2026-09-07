import { Alert, Anchor, Box, Button, Code, CopyButton, Group, Loader, Modal, Paper, Stack, Text, Title, Tooltip } from "@mantine/core";
import { ArrowsClockwise, Check, Copy, WarningCircle } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { getJSON } from "./api";
import { authorizedFetch, useViewer } from "./auth";
import { docs } from "../../links";

type IngestSettings = {
  token_required: boolean;
  suggested_endpoint: string;
  tls_configured: boolean;
  header_name: string;
};

const ingestQueryKey = ["settings", "ingest"] as const;

/** Whether the exporter reaches this instance over TLS. The endpoint may be an
 *  advertised URL, in which case its scheme is the answer; otherwise it is
 *  plaintext unless this process holds the certificate itself. A proxy that
 *  terminates TLS without an https endpoint being advertised is invisible from
 *  here, which is why the hint says so rather than asserting plaintext. */
function overTLS(endpoint: string, tlsConfigured: boolean) {
  return tlsConfigured || /^https:\/\//i.test(endpoint.trim());
}

/** The collector block an engineer actually pastes, with this instance's own
 *  endpoint already in it. Everything else on the page is a fact about the
 *  connection; this is the thing that makes telemetry arrive.
 *
 *  The endpoint is quoted because an advertised IPv6 address arrives as
 *  "[2001:db8::1]:4317", which unquoted YAML reads as a flow sequence. The
 *  token is referenced as ${env:...}, the form the documentation uses and the
 *  only one that is unambiguous when a distribution sets its own default
 *  config scheme. */
function collectorConfig(endpoint: string, header: string, tlsConfigured: boolean) {
  const insecure = overTLS(endpoint, tlsConfigured) ? "" : "\n    tls:\n      insecure: true";
  return `exporters:
  otlp/fanout:
    endpoint: "${endpoint}"
    headers:
      ${header}: "Bearer \${env:FANOUT_INGEST_TOKEN}"${insecure}

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
  return <Stack gap={4}>
    <Text size="sm" fw={600}>{label}</Text>
    <Group gap="xs" wrap="nowrap" align="center">
      <Code style={{ flex: 1, overflowX: "auto" }}>{value}</Code>
      <CopyButton value={value} timeout={1500}>
        {({ copied, copy }) => <Tooltip label={copied ? "Copied" : `Copy ${label.toLowerCase()}`} withArrow>
          <Button variant="subtle" color="gray" size="compact-sm" onClick={copy} aria-label={`Copy ${label.toLowerCase()}`}>
            {copied ? <Check size={15} weight="bold" /> : <Copy size={15} />}
          </Button>
        </Tooltip>}
      </CopyButton>
    </Group>
    {hint && <Text c="dimmed" size="xs">{hint}</Text>}
  </Stack>;
}

export default function Settings() {
  const viewer = useViewer();
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ingestQueryKey, queryFn: () => getJSON<IngestSettings>("/api/settings/ingest") });
  const [confirming, setConfirming] = useState(false);
  const [issued, setIssued] = useState<string | null>(null);

  const rotate = useMutation({
    mutationFn: async () => {
      // A failure between the write and the response leaves the old token dead
      // and the new one unrecoverable, so this case gets its own instruction
      // rather than the browser's "Failed to fetch".
      const response = await authorizedFetch("/api/settings/ingest/rotate-token", { method: "POST" })
        .catch(() => { throw new Error("Fanout could not be reached. A token may still have been issued — reload this page before trying again."); });
      if (!response.ok) throw new Error("Fanout could not issue a new token. Try again.");
      // The plaintext comes back once, under the same key the setup response
      // uses; the rest of the payload is the refreshed settings.
      return response.json() as Promise<IngestSettings & { ingest_token?: string }>;
    },
    onSuccess: (data) => {
      setConfirming(false);
      setIssued(data.ingest_token ?? null);
      // The page has just changed the fact it is describing. Writing the
      // refreshed settings back — without the plaintext, which belongs in no
      // cache — stops the copy below still saying this workspace has no token
      // and offering to issue one, which would destroy the token just issued.
      queryClient.setQueryData<IngestSettings>(ingestQueryKey, {
        token_required: data.token_required,
        suggested_endpoint: data.suggested_endpoint,
        tls_configured: data.tls_configured,
        header_name: data.header_name,
      });
    },
  });

  function dismissToken() {
    setIssued(null);
    // Drops the plaintext from the mutation cache, which otherwise holds it
    // for the life of the tab.
    rotate.reset();
  }

  if (settings.isLoading) return <Loading />;
  if (settings.isError || !settings.data) return <Unavailable onRetry={() => void settings.refetch()} />;

  const { suggested_endpoint: endpoint, header_name: header, tls_configured: tlsConfigured, token_required: hasToken } = settings.data;
  const canRotate = viewer.role === "admin";
  const authorization = `${header}: Bearer <your token>`;

  return <Box component="main" maw={760} mx="auto" px={{ base: "md", sm: "xl" }} pt={{ base: "lg", sm: "xl" }} pb="xl">
    <Stack gap="xl">
      <Box>
        <Title order={1} fz={32} lts="-0.03em">Connect telemetry</Title>
        <Text c="dimmed" mt={2}>Point an OpenTelemetry collector or SDK at this workspace.</Text>
      </Box>

      <Paper withBorder radius="lg" p="lg">
        <Stack gap="lg">
          <Field label="Endpoint" value={endpoint} hint={overTLS(endpoint, tlsConfigured) ? "OTLP over gRPC, secured with TLS." : "OTLP over gRPC. Fanout serves this endpoint in plaintext, so an exporter needs tls.insecure unless a proxy in front of it terminates TLS."} />
          <Field label="Authorization" value={authorization} hint="Send the token your workspace issued. Fanout stores only its hash, so it cannot show you an existing one." />
        </Stack>
      </Paper>

      <Stack gap="sm">
        <Box>
          <Title order={2} fz={22} lts="-0.02em">Collector configuration</Title>
          <Text c="dimmed" size="sm" mt={2}>Merge these blocks into your collector configuration, alongside the receivers it already has, and set FANOUT_INGEST_TOKEN in its environment.</Text>
        </Box>
        <Paper withBorder radius="lg" p="md">
          <Stack gap="sm">
            <Code block style={{ fontSize: 12 }}>{collectorConfig(endpoint, header, tlsConfigured)}</Code>
            <Group justify="flex-end">
              <CopyButton value={collectorConfig(endpoint, header, tlsConfigured)} timeout={1500}>
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
          <Title order={2} fz={22} lts="-0.02em">Ingest token</Title>
          <Text c="dimmed" size="sm" mt={2}>
            {hasToken
              ? "This workspace has a token. Rotating issues a new one and stops the current one working immediately."
              : "This workspace has no token yet, so it is rejecting telemetry. Issue one to start accepting data."}
          </Text>
        </Box>
        <Paper withBorder radius="lg" p="lg">
          {issued
            ? <Stack gap="sm">
                <Text size="sm" fw={600}>Your new token</Text>
                <Code block>{`${header}: Bearer ${issued}`}</Code>
                <Text c="dimmed" size="xs">Fanout shows this once and keeps only its hash. Store it with your collector secrets before closing this, then update any collector still using the old token. Lose it and the only way back is another rotation.</Text>
                <Group>
                  <CopyButton value={`${header}: Bearer ${issued}`} timeout={1500}>
                    {({ copied, copy }) => <Button variant="light" size="compact-sm" leftSection={copied ? <Check size={15} weight="bold" /> : <Copy size={15} />} onClick={copy}>{copied ? "Copied" : "Copy header"}</Button>}
                  </CopyButton>
                  <Button variant="subtle" color="gray" size="compact-sm" onClick={dismissToken}>Done</Button>
                </Group>
              </Stack>
            : canRotate
              ? <Group justify="space-between" align="center">
                  <Text size="sm" c="dimmed">{hasToken ? "Rotate when a token may have been exposed." : "No token issued yet."}</Text>
                  <Button variant={hasToken ? "default" : "filled"} leftSection={<ArrowsClockwise size={15} weight="bold" />} onClick={() => { rotate.reset(); setConfirming(true); }}>
                    {hasToken ? "Rotate token" : "Issue token"}
                  </Button>
                </Group>
              : <Text size="sm" c="dimmed">An administrator issues and rotates the ingest token.</Text>}
        </Paper>
      </Stack>
      <Text c="dimmed" size="xs">
        <Anchor href={docs.ingest} target="_blank" rel="noopener noreferrer" inherit>Sending telemetry</Anchor> covers SDK setup, the collector, and the OTLP/HTTP endpoint.
      </Text>
    </Stack>

    <Modal opened={confirming} onClose={() => !rotate.isPending && setConfirming(false)} title={hasToken ? "Rotate the ingest token?" : "Issue an ingest token?"} centered radius="md">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {hasToken
            ? "The current token stops working the moment the new one is issued. Any collector still sending with it will be rejected until you update it. Fanout shows the new token once and stores only its hash."
            : "Fanout will show the new token once, and store only its hash."}
        </Text>
        {rotate.isError && <Alert color="bad" radius="md" icon={<WarningCircle size={18} weight="fill" />}>{rotate.error instanceof Error ? rotate.error.message : "Fanout could not issue a new token."}</Alert>}
        <Group justify="flex-end" gap="sm">
          <Button variant="default" size="sm" onClick={() => { rotate.reset(); setConfirming(false); }} disabled={rotate.isPending}>Cancel</Button>
          <Button color={hasToken ? "bad" : undefined} size="sm" loading={rotate.isPending} onClick={() => rotate.mutate()}>
            {hasToken ? "Rotate token" : "Issue token"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  </Box>;
}

function Loading() {
  return <Group justify="center" mih="50vh" gap="sm"><Loader size="sm" /><Text c="dimmed" size="sm">Loading connection details…</Text></Group>;
}

/* A failed load is not a slow one: a spinner here reads as still working. */
function Unavailable({ onRetry }: { onRetry: () => void }) {
  return <Group justify="center" mih="50vh">
    <Alert color="bad" radius="md" icon={<WarningCircle size={18} weight="fill" />} maw={420}>
      <Stack gap="sm" align="flex-start">
        <Text size="sm">Fanout could not load this workspace&apos;s connection details.</Text>
        <Button size="compact-sm" variant="light" color="bad" onClick={onRetry}>Try again</Button>
      </Stack>
    </Alert>
  </Group>;
}
