import { describe, it, expect, vi, beforeEach } from "vitest";
import { z } from "zod";
import { api, ApiError, setAccessToken } from "./api-client";

const schema = z.object({ ok: z.boolean() });

beforeEach(() => {
  setAccessToken(null);
  vi.restoreAllMocks();
});

describe("api()", () => {
  it("parses a valid response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 })),
    );
    await expect(api("/x", schema)).resolves.toEqual({ ok: true });
  });

  it("throws ApiError with status on non-2xx (non-401)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("nope", { status: 500 })),
    );
    await expect(api("/x", schema)).rejects.toMatchObject({ status: 500 } satisfies Partial<ApiError>);
  });

  it("throws ApiError when the body fails schema validation", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ ok: "yes" }), { status: 200 })),
    );
    await expect(api("/x", schema)).rejects.toBeInstanceOf(ApiError);
  });

  it("refreshes once on 401 then retries", async () => {
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        calls.push(url);
        if (url === "/api/auth/refresh") return new Response(null, { status: 200 });
        if (calls.filter((u) => u === "/x").length === 1) return new Response(null, { status: 401 });
        return new Response(JSON.stringify({ ok: true }), { status: 200 });
      }),
    );
    await expect(api("/x", schema)).resolves.toEqual({ ok: true });
    expect(calls).toContain("/api/auth/refresh");
  });
});
