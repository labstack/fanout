import { Badge, Group } from "@mantine/core";

export function EditChips({ labels }: { labels: string[] }) {
  if (!labels.length) return null;
  return <Group gap={4} data-edit-chips>{labels.map((label, index) => <Badge key={index} color="gray" variant="light" size="sm" tt="none" maw="100%" title={label} style={{ color: "var(--mantine-color-text)", height: "auto", whiteSpace: "normal", overflowWrap: "anywhere" }}>{label}</Badge>)}</Group>;
}
