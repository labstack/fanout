import { statValue } from "../../../../panels/frame";
import type { Panel, PanelResult } from "../../../../panels/types";

export function previousStatValue(panel: Panel, result: PanelResult): number | null {
  if (!result.previous || ((panel.reduce ?? "window") === "window" && !result.previous.totals)) return null;
  return statValue(panel, result.previous);
}
