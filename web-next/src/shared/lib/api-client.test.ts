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

  it("refreshes once on 401, applies the new token, then retries with it", async () => {
    const calls: { url: string; auth: string | null }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        const auth = init?.headers instanceof Headers ? init.headers.get("Authorization") : null;
        calls.push({ url, auth });
        if (url === "/api/auth/refresh") {
          return new Response(JSON.stringify({ access_token: "NEW" }), { status: 200 });
        }
        if (url === "/x") {
          // First call carries no/old token and must 401. The retry only
          // succeeds if it actually presents the freshly-refreshed bearer —
          // this is what catches a refresh() that discards the new token.
          return auth === "Bearer NEW"
            ? new Response(JSON.stringify({ ok: true }), { status: 200 })
            : new Response(null, { status: 401 });
        }
        return new Response(null, { status: 404 });
      }),
    );

    await expect(api("/x", schema)).resolves.toEqual({ ok: true });

    const xCalls = calls.filter((c) => c.url === "/x");
    expect(xCalls).toHaveLength(2);
    expect(xCalls[0]?.auth).toBeNull();
    expect(xCalls[1]?.auth).toBe("Bearer NEW");
    expect(calls.some((c) => c.url === "/api/auth/refresh")).toBe(true);
  });

  it("surfaces the original 401 when refresh succeeds but the body has no access_token", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url === "/api/auth/refresh") return new Response(JSON.stringify({}), { status: 200 });
        return new Response(null, { status: 401 });
      }),
    );
    await expect(api("/x", schema)).rejects.toMatchObject({ status: 401 } satisfies Partial<ApiError>);
  });
});
