import { Modal, Stack, TextInput } from "@mantine/core";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { Panel } from "../../../panels/types";
import { ApiError } from "../api-error";
import { ExplorePage } from "../explore/page";
import { defaultRange, type ExploreSearch } from "../explore/search";
import { createDashboard, dashboardsKey } from "./api";

/** A dashboard holds at least one panel, so it is created together with its first one (proposal 0004, Decision 1). */
export function NewDashboard({ onClose, onCreated }: { onClose(): void; onCreated(dashboardId: string): void }) {
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [search, setSearch] = useState<ExploreSearch>({ q: "", tab: "query", range: defaultRange });
  const named = name.trim();

  const create = async (panel: Panel) => {
    if (!named) throw new Error("Name the dashboard first.");
    const time = search.from && search.to ? { from: search.from, to: search.to } : { range: search.range };
    try {
      const record = await createDashboard({ version: 1, name: named, time, panels: [{ ...panel, id: "panel" }] });
      void client.invalidateQueries({ queryKey: dashboardsKey });
      onCreated(record.id);
    } catch (failure) {
      if (failure instanceof ApiError && failure.problems.length > 0) throw new Error(failure.problems.map(p => p.message).join(" "));
      throw failure;
    }
  };

  // Escape first reaches the editor, which closes its completion list; only an unhandled Escape closes the dialog.
  return <Modal opened onClose={onClose} closeOnEscape={false} title="New dashboard" size="min(1280px, 100vw - 32px)"
    onKeyDown={event => { if (event.key === "Escape" && !event.defaultPrevented && !event.nativeEvent.isComposing) onClose(); }}>
    <Stack gap={0}>
      <TextInput label="Dashboard name" px="lg" value={name} onChange={event => setName(event.currentTarget.value)} data-autofocus required />
      <ExplorePage search={search} onSearch={setSearch}
        context={{ spec: { version: 1, name: named || "New dashboard", time: { range: search.range }, panels: [] }, vars: {}, save: { label: "Create dashboard", run: create } }} />
    </Stack>
  </Modal>;
}
