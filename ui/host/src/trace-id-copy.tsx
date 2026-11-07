import { ActionIcon, Box, CopyButton, Group } from "@mantine/core";
import { Check, Copy } from "@phosphor-icons/react";
import type { ReactNode } from "react";

/** A shortened identifier always copies its complete machine value. */
export function TraceIdCopy({ value, children }: { value: string; children: ReactNode }) {
  return <Group gap={4} wrap="nowrap"><Box miw={0} flex={1}>{children}</Box>
    <CopyButton value={value}>{({copied, copy}) => <ActionIcon size="xs" variant="subtle" color="gray" aria-label={copied ? "Trace ID copied" : "Copy trace ID"} title={value} onClick={event => {event.stopPropagation(); copy();}}>{copied ? <Check size={12} /> : <Copy size={12} />}</ActionIcon>}</CopyButton>
  </Group>;
}
