import { Center, Group, Text, ThemeIcon, Box } from "@mantine/core";
import type { ReactNode } from "react";
export function EmptyState({
  icon,
  title,
  children,
  tall = false,
}: {
  icon: ReactNode;
  title: string;
  children: ReactNode;
  tall?: boolean;
}) {
  return (
    <Center mih={tall ? 220 : 130} p="xl">
      <Group wrap="nowrap">
        <ThemeIcon variant="light" size="xl" radius="md">
          {icon}
        </ThemeIcon>
        <Box>
          <Text fw={700} size="sm">
            {title}
          </Text>
          <Text c="dimmed" size="xs" mt={3}>
            {children}
          </Text>
        </Box>
      </Group>
    </Center>
  );
}
