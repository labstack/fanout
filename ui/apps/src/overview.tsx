import { Badge, Box, Group, Progress, SimpleGrid, Table, Text } from "@mantine/core";
import { Pulse } from "@phosphor-icons/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, MetaFooter, Metric, PageControls, ViewHeader, ViewShell, ViewStatus, healthColor, usePagedItems } from "./components";
import type { Overview, Result, ServiceHealth } from "./contracts";
import { duration, integer, percent, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";

function OverviewApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Overview>>("Fanout system health");
  return <ViewShell dark={host?.theme === "dark"}>
    <ViewHeader eyebrow="Live system view" title="System health" summary={result?.summary} onRefresh={() => callTool("observability_overview")} disabled={!app} />
    <ViewStatus error={toolError ?? (error ? "This view could not be loaded. Please try again." : null)} loading={!result && !error && !toolError ? "Loading system health…" : undefined} />
    {result && <OverviewBody result={result} onService={(service) => askAbout(app, `Investigate the ${service} service. Explain its errors and latency.`)} />}
  </ViewShell>;
}

function OverviewBody({ result, onService }: { result: Result<Overview>; onService: (service: string) => void }) {
  const { data } = result;
  const total = Math.max(data.service_count, 1);
  const services = usePagedItems(data.services, 6);
  return <>
    <SimpleGrid cols={{ base: 1, xs: 3 }} spacing="sm" px={{ base: "md", sm: "lg" }} pb="md">
      <Metric label="Services" value={integer.format(data.service_count)} />
      <Metric label="Operations" value={integer.format(data.total_spans)} />
      <Metric label="Error rate" value={percent(data.error_rate)} color={healthColor(data.health)} />
    </SimpleGrid>
    <Box px={{ base: "md", sm: "lg" }} pb="md">
      <Progress.Root size="lg" aria-label="Service health distribution">
        <Progress.Section value={data.counts.healthy / total * 100} color="ok"><Progress.Label>{data.counts.healthy}</Progress.Label></Progress.Section>
        <Progress.Section value={data.counts.degraded / total * 100} color="warn"><Progress.Label>{data.counts.degraded}</Progress.Label></Progress.Section>
        <Progress.Section value={data.counts.unhealthy / total * 100} color="bad"><Progress.Label>{data.counts.unhealthy}</Progress.Label></Progress.Section>
      </Progress.Root>
      <Group mt="xs" gap="lg"><Legend color="ok" text={`${data.counts.healthy} healthy`} /><Legend color="warn" text={`${data.counts.degraded} degraded`} /><Legend color="bad" text={`${data.counts.unhealthy} unhealthy`} /></Group>
    </Box>
    {data.services.length === 0 ? <EmptyState icon={<Pulse size={20} weight="duotone" />} title="No activity in this window">Services will appear as data begins to arrive.</EmptyState> : <><Table.ScrollContainer minWidth={560}><Table striped highlightOnHover verticalSpacing="sm">
      <Table.Thead><Table.Tr><Table.Th>Service</Table.Th><Table.Th>Traffic</Table.Th><Table.Th>P95</Table.Th><Table.Th>Errors</Table.Th></Table.Tr></Table.Thead>
      <Table.Tbody>{services.pageItems.map((service) => <ServiceRow key={service.service} service={service} onClick={() => onService(service.service)} />)}</Table.Tbody>
    </Table></Table.ScrollContainer><PageControls {...services} onChange={services.setPage} /></>}
    <MetaFooter left={windowLabel(result.provenance.window)} right={`Updated ${new Date(result.provenance.generated_at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`} />
  </>;
}

function Legend({ color, text }: { color: string; text: string }) {
  return <Group gap={6}><Box w={8} h={8} bg={color} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{text}</Text></Group>;
}

function ServiceRow({ service, onClick }: { service: ServiceHealth; onClick: () => void }) {
  return <Table.Tr onClick={onClick} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") onClick(); }} tabIndex={0} style={{ cursor: "pointer" }}>
    <Table.Td><Badge color={healthColor(service.health)} variant="light" tt="none">{service.service}</Badge></Table.Td>
    <Table.Td>{integer.format(service.spans)}</Table.Td><Table.Td>{duration(service.p95_ms)}</Table.Td><Table.Td>{percent(service.error_rate)}</Table.Td>
  </Table.Tr>;
}

createRoot(document.getElementById("root")!).render(<StrictMode><OverviewApp /></StrictMode>);
