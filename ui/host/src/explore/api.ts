import type { Query, Variable, Viz } from "../../../panels/types";
import type { FixIt, TextSpan } from "../api-error";
import { request } from "../dashboards/api";

export type Notice = { path: string; message: string; span?: TextSpan; fix_its?: FixIt[] };
export type VizCompat = { viz: Viz; requires: ("min_max" | "thresholds")[] };
export type VizEdit = { viz: Viz; text: string };
export type Parsed = { query: Query; canonical: string; viz: { requested?: string; compatible: VizCompat[]; edits: VizEdit[] }; notices: Notice[] };
export type CompletionItem = { label: string; insert: string; kind: string; detail: string };
export type Completion = { replace: TextSpan; items: CompletionItem[]; schema: { notice?: string; window: string; sampled: boolean; warm: boolean; recent: boolean; error?: boolean } };

/** `window` names a relative range whose cached schema checks keys; `namespace` narrows it. */
export type ParseBody = { text: string; source?: string; viz?: string; variables?: Variable[]; window?: string; namespace?: string };
export type CompleteBody = { text: string; cursor: number; source?: string; viz?: string; window?: string; from?: string; to?: string };

// Problems arrive as an ApiError with code invalid_pitotql; spans index the text.
export const parsePitotql = (body: ParseBody, signal?: AbortSignal) => request<Parsed>("/api/pitotql/parse", { method: "POST", json: body, signal });
export const completePitotql = (body: CompleteBody, signal?: AbortSignal) => request<Completion>("/api/pitotql/complete", { method: "POST", json: body, signal });

/** `where` and `measures` give each stored entry as PitotQL, in stored order. */
export type Formatted = { text: string; exactness: string; editable: boolean; reason?: string; requires: string[]; notices: Notice[]; where: string[]; measures: string[] };
// Prints a structured query as PitotQL; problems arrive as an ApiError with code invalid_spec.
export const formatPitotql = (body: { query: Query; viz?: string }, signal?: AbortSignal) => request<Formatted>("/api/pitotql/format", { method: "POST", json: body, signal });
