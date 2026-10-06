// Adapted from ui/apps/src/components.tsx. Keep Bun workspace boundaries; consolidate in M3.
import { Button, Center, Group, Text, ThemeIcon, Box } from "@mantine/core";
import { useEffect, useState, type ReactNode } from "react";
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
export function usePagedItems<T>(items: T[], pageSize = 8) {
  const [page, setPage] = useState(1);
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [page, totalPages]);
  const start = (page - 1) * pageSize;
  return {
    page,
    setPage,
    totalPages,
    pageItems: items.slice(start, start + pageSize),
    from: items.length ? start + 1 : 0,
    to: Math.min(start + pageSize, items.length),
    total: items.length,
  };
}
export function PageControls({
  page,
  totalPages,
  from,
  to,
  total,
  onChange,
}: {
  page: number;
  totalPages: number;
  from: number;
  to: number;
  total: number;
  onChange: (page: number) => void;
}) {
  if (totalPages <= 1) return null;
  return (
    <Group justify="space-between" mt="xs">
      <Text c="dimmed" size="xs">
        {from}–{to} of {total}
      </Text>
      <Group gap={4}>
        {[
          { label: "First page", page: 1, disabled: page === 1 },
          { label: "Previous page", page: page - 1, disabled: page === 1 },
          { label: "Next page", page: page + 1, disabled: page === totalPages },
          {
            label: "Last page",
            page: totalPages,
            disabled: page === totalPages,
          },
        ].map((item) => (
          <Button
            key={item.label}
            size="compact-xs"
            variant="subtle"
            aria-label={item.label}
            disabled={item.disabled}
            onClick={() => onChange(item.page)}
          >
            {item.label}
          </Button>
        ))}
      </Group>
    </Group>
  );
}
