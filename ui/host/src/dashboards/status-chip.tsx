import { Badge } from "@mantine/core";
import type { Status } from "../../../panels/types";

const glyph: Record<Status, string> = { ok: "●", warn: "■", bad: "◆" };
const word: Record<Status, string> = { ok: "Healthy", warn: "Degraded", bad: "Unhealthy" };

/** Fanout's health shapes: the glyph is the second channel so state reads
 *  without colour. */
export function StatusChip({ status }: { status: Status }) {
  return <Badge color={status} variant="light" tt="none" leftSection={<span aria-hidden>{glyph[status]}</span>} style={{ minWidth: "max-content" }}>{word[status]}</Badge>;
}
