import type { PanelResult } from "../../../panels/types";

/** The dashboard asks /api/queries/run for NDJSON (one result per line, written
 *  as each panel finishes); plain JSON requests still get `{results: [...]}`. */
const streamType = "application/x-ndjson";

type Body = { text(): Promise<string>; headers(): Record<string, string> };

export function isStream(response: Body): boolean {
  return (response.headers()["content-type"] ?? "").includes(streamType);
}

/** Every panel result in a /api/queries/run response, in arrival order. */
export async function panelResults(response: Body): Promise<PanelResult[]> {
  const text = await response.text();
  if (!isStream(response)) return (JSON.parse(text) as { results: PanelResult[] }).results;
  return text.split("\n").filter(line => line.trim()).map(line => JSON.parse(line) as PanelResult);
}

/** Serializes results in the same format the response used. */
export function encodePanelResults(results: PanelResult[], stream: boolean): string {
  return stream ? results.map(result => JSON.stringify(result) + "\n").join("") : JSON.stringify({ results });
}
