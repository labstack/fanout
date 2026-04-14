export class ApiError extends Error {
  status: number;
  detail: string;

  constructor(status: number, detail: string) {
    super(detail);
    this.name = "ApiError";
    this.status = status;
    this.detail = detail;
  }
}

let apiToken: string | null = null;

export function setApiToken(t: string | null) {
  apiToken = t;
}

export function getApiToken(): string | null {
  return apiToken;
}

async function fetchWithAuth(
  path: string,
  opts: RequestInit = {},
): Promise<Response> {
  const { headers: optsHeaders, ...rest } = opts;
  return fetch(path, {
    ...rest,
    headers: {
      "Content-Type": "application/json",
      ...(apiToken ? { Authorization: `Bearer ${apiToken}` } : {}),
      ...(optsHeaders as Record<string, string>),
    },
  });
}

export async function api<T>(
  path: string,
  opts: RequestInit = {},
): Promise<T> {
  const res = await fetchWithAuth(path, opts);

  if (!res.ok) {
    const body = await res.json().catch(() => ({ detail: res.statusText }));
    throw new ApiError(res.status, body.detail ?? res.statusText);
  }

  if (res.status === 204) return undefined as T;
  return res.json();
}
