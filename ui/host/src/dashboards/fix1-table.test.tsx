import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { panelFragment } from "../../../panels/fragment";
import trace from "../../tests/go-fragments/trace.json";
import { RowPanel } from "./viz/row-panel";
it("fits the real trace projection to its card with ellipsis and complete time titles", async () => {
  const fragment = panelFragment(trace), node = document.createElement("div"), root = createRoot(node);
  try {
    await act(async () => root.render(<MantineProvider><RowPanel panel={fragment.dashboard.panels[0]} result={fragment.results[0]} dark={false} height={360} /></MantineProvider>));
    const table = node.querySelector<HTMLTableElement>("table")!;
    expect(table.style.tableLayout).toBe("fixed"); expect(table.style.width).toBe("100%");
    const time = table.querySelector<HTMLElement>('td[data-field="start"] > *')!;
    expect(time.style.textOverflow).not.toBe("ellipsis");
    expect(time.style.whiteSpace).toBe("nowrap");
    expect(table.querySelector<HTMLTableColElement>('col[data-field="start"]')?.style.width).toBe("146px");
    expect(table.querySelector<HTMLElement>('td[data-field="operation"] > *')?.style.textOverflow).toBe("ellipsis");
    expect(time.title).toBe("2026-10-07T19:15:01.000Z");
  } finally { await act(async () => root.unmount()); }
});
