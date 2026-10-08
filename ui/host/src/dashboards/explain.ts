export type ExplainContext = { dashboard_id?: string; version?: number; panel_id: string; title: string; from_ms?: number; to_ms?: number; vars: Record<string, string | string[]>; error?: string; spec?: unknown };

export function explainPrompt(c: ExplainContext): string {
  const window = Number.isFinite(c.from_ms) && Number.isFinite(c.to_ms) ? `${new Date(c.from_ms!).toISOString()} to ${new Date(c.to_ms!).toISOString()}` : 'No executed window is available; say that explicitly.';
  return `Explain the panel ${JSON.stringify(c.title)} (panel id ${c.panel_id}${c.dashboard_id ? `, dashboard id ${c.dashboard_id}, version ${c.version}` : ''}). Observed window: ${window}. Resolved variables: ${JSON.stringify(c.vars)}. ${c.error ? `Observed error: ${JSON.stringify(c.error)}. ` : ''}${c.spec ? `Panel specification: ${JSON.stringify(c.spec)}. ` : ''}Answer what it shows and what evidence supports unusual behavior. This is an explanation request. Do not create, edit, replace or restore a dashboard. If it fails, explain the error and suggest a correction without saving it.`;
}
