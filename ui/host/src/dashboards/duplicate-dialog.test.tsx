import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSpec } from "../../../panels/types";
import { DuplicateDialog } from "./duplicate-dialog";

const spec: DashboardSpec = { version: 1, name: "Checkout", time: { range: "3h" }, panels: [{ id: "p", title: "Requests", viz: "stat", query: { from: "spans", measures: ["count()"] } }] };
const fetchMock = vi.fn<typeof fetch>();
const cleanups: (() => void)[] = [];
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.stubGlobal("fetch", fetchMock); fetchMock.mockReset(); fetchMock.mockResolvedValue(Response.json({ id: "copy", version: 1 }, { status: 201 })); });
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

describe("DuplicateDialog", () => {
  it("copies the spec under a new name and opens the copy", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), client = new QueryClient();
    const onCreated = vi.fn();
    await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><DuplicateDialog spec={spec} onClose={vi.fn()} onCreated={onCreated} /></QueryClientProvider></MantineProvider>));
    cleanups.push(() => { root.unmount(); client.clear(); });
    const ui = within(document.body);
    const save = () => ui.getByRole<HTMLButtonElement>("button", { name: "Duplicate" });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "New dashboard name" }), { target: { value: "Checkout" } }); });
    expect(save().disabled).toBe(true);
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "New dashboard name" }), { target: { value: "Checkout EU" } }); });
    await act(async () => save().click());
    await vi.waitFor(() => expect(onCreated).toHaveBeenCalledWith("copy"));
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body))).toEqual({ spec: { ...spec, name: "Checkout EU" } });
  });
});
