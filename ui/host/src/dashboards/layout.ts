import type { Panel } from "../../../panels/types";
export const rowHeight = 40;
export const margin = 12;
export const pixels = (rows: number) => rows * rowHeight + (rows - 1) * margin;
export const defaultRows = (panel: Panel) => ({ s: 3, m: 6, l: 10 }[panel.height ?? (panel.viz === "service_map" ? "l" : "m")]);
export const fragmentPanelHeight = (panel: Panel) => Math.max(panel.viz === "service_map" ? 460 : panel.viz === "stat" ? pixels(4) : 0, pixels(panel.grid?.h ?? defaultRows(panel)));
