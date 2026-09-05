import { Button, Stack, Text } from "@mantine/core";
import { Sparkle } from "@phosphor-icons/react";
import { windowName } from "./data";
import type { WidgetBodyProps } from "./widget-card";

export default function AssistantWidget({ filters, agentAvailable, onOpenChat }: WidgetBodyProps) {
  if (!agentAvailable) return <Text c="dimmed" size="sm">Configure an AI provider to enable this view. The rest of this dashboard remains available.</Text>;
  const window = windowName(filters.window);
  const questions = [`Summarize the last ${window}`, `What changed in the last ${window}?`, "Which service needs attention right now?"];
  return <Stack gap="xs" align="flex-start">
    {questions.map((question) => <Button key={question} variant="default" size="xs" radius="xl" leftSection={<Sparkle size={13} weight="fill" />} onClick={() => onOpenChat(question)}>{question}</Button>)}
  </Stack>;
}
