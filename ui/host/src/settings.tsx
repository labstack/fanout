import { Alert, Anchor, Box, Button, Code, CopyButton, Group, Loader, Modal, Paper, Stack, Text, Title, Tooltip } from "@mantine/core";
import { ArrowsClockwise, Check, Copy, WarningCircle } from "@phosphor-icons/react";
import { useMutation, useQuery } from "@tanstack/react-query";
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

/** The collector block an engineer actually pastes, with this instance's own
 *  endpoint already in it. Everything else on the page is a fact about the
 *  connection; this is the thing that makes telemetry arrive. */
function collectorConfig(endpoint: string, header: string, tls: boolean) {
  return `exporters:
  otlp/fanout:
    endpoint: ${endpoint}
    headers:
      ${header.toLowerCase()}: "Bearer $\{FANOUT_INGEST_TOKEN}"${tls ? "" : "\n    tls:\n      insecure: true"}

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
  const settings = useQuery({ queryKey: ingestQueryKey, queryFn: () => getJSON<IngestSettings>("/api/settings/ingest") });
  const [confirming, setConfirming] = useState(false);
  const [issued, setIssued] = useState<string | null>(null);

  const rotate = useMutation({
    mutationFn: async () => {
      const response = await authorizedFetch("/api/settings/ingest/rotate-token", { method: "POST" });
      if (!response.ok) throw new Error("Fanout could not issue a new token. Try again.");
      // The plaintext comes back once, under the same key the setup response
      // uses; the rest of the payload is the refreshed settings.
      return response.json() as Promise<IngestSettings & { ingest_token?: string }>;
    },
    onSuccess: (data) => {
      setConfirming(false);
      setIssued(data.ingest_token ?? null);
    },
  });

  if (settings.isLoading) return <Center label="Loading connection details…" />;
  if (settings.isError || !settings.data) return <Center label="Connection details are unavailable. Try refreshing." />;

  const { suggested_endpoint: endpoint, header_name: header, tls_configured: tls, token_required: hasToken } = settings.data;
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
          <Field label="Endpoint" value={endpoint} hint={tls ? "OTLP over gRPC, TLS terminated at the edge." : "OTLP over gRPC. This instance serves plaintext, so the exporter needs tls.insecure."} />
          <Field label="Authorization" value={authorization} hint="Send the token your workspace issued. Fanout stores only its hash, so it cannot show you an existing one." />
        </Stack>
      </Paper>

      <Stack gap="sm">
        <Box>
          <Title order={2} fz={22} lts="-0.02em">Collector configuration</Title>
          <Text c="dimmed" size="sm" mt={2}>Paste this into your collector and set FANOUT_INGEST_TOKEN in its environment.</Text>
        </Box>
        <Paper withBorder radius="lg" p="md">
          <Stack gap="sm">
            <Code block style={{ fontSize: 12 }}>{collectorConfig(endpoint, header, tls)}</Code>
            <Group justify="flex-end">
              <CopyButton value={collectorConfig(endpoint, header, tls)} timeout={1500}>
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
                <Text c="dimmed" size="xs">Fanout shows this once. Store it with your collector secrets, then update any collector still using the old token.</Text>
                <Group>
                  <CopyButton value={`${header}: Bearer ${issued}`} timeout={1500}>
                    {({ copied, copy }) => <Button variant="light" size="compact-sm" leftSection={copied ? <Check size={15} weight="bold" /> : <Copy size={15} />} onClick={copy}>{copied ? "Copied" : "Copy header"}</Button>}
                  </CopyButton>
                  <Button variant="subtle" color="gray" size="compact-sm" onClick={() => setIssued(null)}>Done</Button>
                </Group>
              </Stack>
            : canRotate
              ? <Group justify="space-between" align="center">
                  <Text size="sm" c="dimmed">{hasToken ? "Rotate when a token may have been exposed." : "No token issued yet."}</Text>
                  <Button variant={hasToken ? "default" : "filled"} leftSection={<ArrowsClockwise size={15} weight="bold" />} onClick={() => setConfirming(true)}>
                    {hasToken ? "Rotate token" : "Issue token"}
                  </Button>
                </Group>
              : <Text size="sm" c="dimmed">An administrator issues and rotates the ingest token.</Text>}
        </Paper>
      </Stack>
      <Text c="dimmed" size="xs">
        <Anchor href={docs.ingest} target="_blank" rel="noopener noreferrer" inherit>Ingest documentation</Anchor> covers SDK setup, batching and the OTLP/HTTP endpoint.
      </Text>
    </Stack>

    <Modal opened={confirming} onClose={() => !rotate.isPending && setConfirming(false)} title={hasToken ? "Rotate the ingest token?" : "Issue an ingest token?"} centered radius="md">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {hasToken
            ? "The current token stops working the moment the new one is issued. Any collector still sending with it will be rejected until you update it."
            : "Fanout will show the new token once, and store only its hash."}
        </Text>
        {rotate.isError && <Alert color="bad" radius="md" icon={<WarningCircle size={18} weight="fill" />}>{rotate.error instanceof Error ? rotate.error.message : "Fanout could not issue a new token."}</Alert>}
        <Group justify="flex-end" gap="sm">
          <Button variant="default" size="sm" onClick={() => setConfirming(false)} disabled={rotate.isPending}>Cancel</Button>
          <Button color={hasToken ? "bad" : undefined} size="sm" loading={rotate.isPending} onClick={() => rotate.mutate()}>
            {hasToken ? "Rotate token" : "Issue token"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  </Box>;
}

function Center({ label }: { label: string }) {
  return <Group justify="center" mih="50vh" gap="sm"><Loader size="sm" /><Text c="dimmed" size="sm">{label}</Text></Group>;
}
