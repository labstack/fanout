export type ExplainContext = { dashboard_id?: string; version?: number; panel_id: string; title: string; from_ms?: number; to_ms?: number; vars: Record<string, string | string[]>; error?: string; status?: string; diagnosis?: string; stale_since?: number; truncated?: boolean; note?: string; spec?: unknown };

export function explainPrompt(c: ExplainContext): string {
  const window = Number.isFinite(c.from_ms) && Number.isFinite(c.to_ms) ? `${new Date(c.from_ms!).toISOString()} to ${new Date(c.to_ms!).toISOString()}` : 'No executed window is available; say that explicitly.';
  return `Explain the panel ${JSON.stringify(c.title)} (panel id ${c.panel_id}${c.dashboard_id ? `, dashboard id ${c.dashboard_id}, version ${c.version}` : ''}). Observed window (millisecond-truncated): ${window}. Resolved variables: ${JSON.stringify(c.vars)}. Observed state: ${JSON.stringify({ status: c.status, diagnosis: c.diagnosis, stale_since: c.stale_since, truncated: c.truncated, note: c.note })}. ${c.error ? `Observed error: ${JSON.stringify(c.error)}. ` : ''}${c.spec ? `Panel specification: ${JSON.stringify(c.spec)}. ` : ''}Answer what it shows and what evidence supports unusual behavior. This is an explanation request. Do not create, edit, replace or restore a dashboard. If it fails, explain the error and suggest a correction without saving it.`;
}

/** Separate explicit edit request; never used by Explain. */
export function fixPrompt(c: ExplainContext): string {
  return `Please fix the panel ${JSON.stringify(c.title)} (panel id ${c.panel_id}, dashboard id ${c.dashboard_id}, version ${c.version}). Observed error: ${JSON.stringify(c.error)}. Resolved variables: ${JSON.stringify(c.vars)}. This is an explicit dashboard edit request. Get the dashboard and correct only this panel.`;
}
