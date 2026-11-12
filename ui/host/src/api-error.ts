export type TextSpan = { start: number; end: number };
export type FixIt = { label: string; span: TextSpan; text: string };
export type Problem = { path: string; message: string; hint?: string; span?: TextSpan; fix_its?: FixIt[] };

export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly problems: Problem[] = [], readonly code: string = "") {
    super(message);
    // Read alias for responses saved or served before the language rename.
    if (this.code === "invalid_fanql") this.code = "invalid_pitotql";
  }
}
