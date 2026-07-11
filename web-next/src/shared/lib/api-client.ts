import { z, type ZodType } from "zod";

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

// In-memory only — never persisted (localStorage/sessionStorage/cookies are
// all off-limits for the access token; refresh relies on the httpOnly cookie
// sent via credentials: "include").
let accessToken: string | null = null;
export function setAccessToken(token: string | null): void {
  accessToken = token;
}

// Body of a successful refresh — untrusted, parsed defensively.
const refreshResponseSchema = z.object({ access_token: z.string().min(1) });

// Dedupe concurrent refreshes: one in-flight refresh shared by all 401s.
let refreshInFlight: Promise<boolean> | null = null;
function refresh(): Promise<boolean> {
  refreshInFlight ??= fetch("/api/auth/refresh", { method: "POST", credentials: "include" })
    .then(async (r) => {
      if (!r.ok) return false;
      const json: unknown = await r.json().catch(() => undefined);
      const parsed = refreshResponseSchema.safeParse(json);
      if (!parsed.success) return false;
      setAccessToken(parsed.data.access_token);
      return true;
    })
    .catch(() => false)
    .finally(() => {
      refreshInFlight = null;
    });
  return refreshInFlight;
}

function headers(init?: RequestInit): Headers {
  const h = new Headers(init?.headers);
  if (accessToken) h.set("Authorization", `Bearer ${accessToken}`);
  return h;
}

async function once(path: string, init?: RequestInit): Promise<Response> {
  return fetch(path, { ...init, headers: headers(init), credentials: "include" });
}

export async function api<T>(path: string, schema: ZodType<T>, init?: RequestInit): Promise<T> {
  let res = await once(path, init);
  if (res.status === 401 && (await refresh())) {
    res = await once(path, init);
  }
  if (!res.ok) throw new ApiError(`${path} → ${res.status}`, res.status);

  const json: unknown = await res.json().catch(() => undefined);
  const parsed = schema.safeParse(json);
  if (!parsed.success) {
    console.error(`api: schema mismatch for ${path}`, parsed.error.issues);
    throw new ApiError(`schema mismatch for ${path}`, res.status);
  }
  return parsed.data;
}
