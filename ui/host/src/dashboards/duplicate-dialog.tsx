import { Notice } from "@labstack/ui";
import { Box,  Button, Group, Modal, Stack, TextInput } from "@mantine/core";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DashboardSpec } from "../../../panels/types";
import { createDashboard, dashboardsKey } from "./api";

/** Copies the whole spec under a new name; the copy has its own history. */
export function DuplicateDialog({ spec, onClose, onCreated }: { spec: DashboardSpec; onClose(): void; onCreated(dashboardId: string): void }) {
  const client = useQueryClient();
  const [name, setName] = useState(`${spec.name} copy`);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const save = async () => {
    setSaving(true); setError(undefined);
    try {
      const record = await createDashboard({ ...spec, name: name.trim() });
      void client.invalidateQueries({ queryKey: dashboardsKey });
      onCreated(record.id);
    } catch (failure) { setError(failure instanceof Error ? failure.message : "Duplicate failed"); }
    finally { setSaving(false); }
  };
  return <Modal opened onClose={() => { if (!saving) onClose(); }} title="Duplicate dashboard">
    <Stack gap="md">
      <TextInput label="New dashboard name" value={name} onChange={event => setName(event.currentTarget.value)} data-autofocus />
      {error && <Box><Notice tone="danger">{error}</Notice></Box>}
      <Group justify="flex-end" gap="xs">
        <Button variant="default" disabled={saving} onClick={onClose}>Cancel</Button>
        <Button loading={saving} disabled={!name.trim() || name.trim() === spec.name} onClick={() => void save()}>Duplicate</Button>
      </Group>
    </Stack>
  </Modal>;
}
