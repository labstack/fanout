import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api-error";
import { completePitotql, parsePitotql } from "./api";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("../auth", () => ({ authorizedFetch: fetchMock }));

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const lastCall = () => fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit];

describe("pitotql api", () => {
  beforeEach(() => fetchMock.mockReset());

  it("parses text and returns the compiled query", async () => {
    const parsed = { query: { from: "spans", measures: ["count()"] }, canonical: "spans | count()", viz: { compatible: [{ viz: "stat", requires: [] }], edits: [] }, notices: [] };
    fetchMock.mockResolvedValue(json(200, parsed));
    const signal = new AbortController().signal;
    await expect(parsePitotql({ text: "spans | count()", viz: "stat" }, signal)).resolves.toEqual(parsed);
    expect(lastCall()[0]).toBe("/api/pitotql/parse");
    expect(lastCall()[1].method).toBe("POST");
    expect(lastCall()[1].signal).toBe(signal);
    expect(JSON.parse(lastCall()[1].body as string)).toEqual({ text: "spans | count()", viz: "stat" });
  });

  it("keeps problem spans and fix-its from an invalid query", async () => {
    const problems = [{ path: "text", message: "unknown field", span: { start: 6, end: 11 }, fix_its: [{ label: "Use service", span: { start: 6, end: 11 }, text: "service" }] }];
    fetchMock.mockResolvedValue(json(400, { code: "invalid_pitotql", message: "The query has problems.", problems }));
    const error = await parsePitotql({ text: "spans where servce:a" }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe("invalid_pitotql");
    expect((error as ApiError).problems).toEqual(problems);
  });

  it("asks for completions at the cursor", async () => {
    fetchMock.mockResolvedValue(json(200, { replace: { start: 0, end: 2 }, items: [], schema: { window: "1h", sampled: false, warm: true, recent: true } }));
    await completePitotql({ text: "sp", cursor: 2, window: "1h" });
    expect(lastCall()[0]).toBe("/api/pitotql/complete");
    expect(JSON.parse(lastCall()[1].body as string)).toEqual({ text: "sp", cursor: 2, window: "1h" });
  });
});
