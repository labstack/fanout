export type ExplainContext = { dashboard_id?: string; version?: number; panel_id: string; title: string; from_ms?: number; to_ms?: number; vars: Record<string, string | string[]>; error?: string; status?: string; diagnosis?: string; stale_since?: number; truncated?: boolean; note?: string; spec?: unknown };

export function explainPrompt(c: ExplainContext): string {
  return `Explain the panel “${c.title}”`;
}

export function fixPrompt(c: ExplainContext): string {
  return `Fix the panel “${c.title}”`;
}

export function panelContext(c: ExplainContext) {
  return [{ description: "Observed panel diagnostics", value: JSON.stringify(c) }];
}
