import { FormEvent, ReactNode, useEffect, useState } from "react";

const tokenKey = "fanout.access-token";
const unauthorizedEvent = "fanout:unauthorized";

let refreshPromise: Promise<string> | null = null;

export function getToken(): string {
  return localStorage.getItem(tokenKey) ?? "";
}

function saveToken(token: string) {
  localStorage.setItem(tokenKey, token);
}

function oauthReturnTo(): string {
  const value = new URLSearchParams(window.location.search).get("return_to");
  if (!value) return "";
  const target = new URL(value, window.location.origin);
  if (target.origin !== window.location.origin || target.pathname !== "/api/auth/oauth/authorize") return "";
  return `${target.pathname}${target.search}`;
}

export function clearSession() {
  localStorage.removeItem(tokenKey);
  window.dispatchEvent(new Event(unauthorizedEvent));
}

async function refreshAccessToken(): Promise<string> {
  if (!refreshPromise) {
    refreshPromise = fetch("/api/auth/refresh", {
      method: "POST",
      credentials: "same-origin",
    })
      .then(async (response) => {
        if (!response.ok) throw new Error(`Session refresh failed (${response.status})`);
        const payload = await response.json() as { access_token?: string };
        if (!payload.access_token) throw new Error("Session refresh returned no access token");
        saveToken(payload.access_token);
        return payload.access_token;
      })
      .finally(() => { refreshPromise = null; });
  }
  return refreshPromise;
}

export async function authorizedFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const request = (token: string) => {
    const headers = new Headers(init.headers);
    if (token) headers.set("Authorization", `Bearer ${token}`);
    return fetch(input, { ...init, headers, credentials: "same-origin" });
  };

  const response = await request(getToken());
  if (response.status !== 401) return response;

  try {
    return await request(await refreshAccessToken());
  } catch {
    clearSession();
    return response;
  }
}

export async function logout() {
  try {
    await fetch("/api/auth/logout", { method: "POST", credentials: "same-origin" });
  } finally {
    clearSession();
  }
}

type Status = { setup_required: boolean; public_read: boolean };
type SetupResult = {
  access_token: string;
  ingest_token?: string;
  ingest_header_name?: string;
  suggested_endpoint?: string;
};

async function jsonRequest(path: string, body?: unknown) {
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.message ?? payload.error ?? `Request failed (${response.status})`);
  return payload;
}

export default function AuthGate({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [token, setToken] = useState(getToken());
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [code, setCode] = useState("");
  const [codeSent, setCodeSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [setupResult, setSetupResult] = useState<SetupResult | null>(null);
  const [copied, setCopied] = useState(false);
  const returnTo = oauthReturnTo();

  useEffect(() => {
    jsonRequest("/api/auth/status").then(setStatus).catch((value) => setError(String(value)));
    const handleUnauthorized = () => setToken("");
    window.addEventListener(unauthorizedEvent, handleUnauthorized);
    return () => window.removeEventListener(unauthorizedEvent, handleUnauthorized);
  }, []);

  useEffect(() => {
    if (token && returnTo) window.location.replace(returnTo);
  }, [returnTo, token]);

  async function copyIngestToken() {
    try {
      await navigator.clipboard.writeText(setupResult?.ingest_token ?? "");
      setCopied(true);
    } catch {
      setError("Clipboard access failed. Select and copy the token manually.");
    }
  }

  if (setupResult?.ingest_token) {
    return (
      <main className="auth-shell">
        <section className="auth-card setup-complete">
          <div className="brand-mark">F</div>
          <p className="eyebrow">SETUP COMPLETE</p>
          <h1>Save your ingest token</h1>
          <p className="muted">Fanout shows this token once. Store it with your collector secrets before continuing.</p>
          <label>OTLP endpoint<code className="token-value">{setupResult.suggested_endpoint ?? "demo.fanout.test:4317"}</code></label>
          <label>Header<code className="token-value">{setupResult.ingest_header_name ?? "x-fanout-ingest-token"}: {setupResult.ingest_token}</code></label>
          {error && <p className="error">{error}</p>}
          <div className="setup-actions">
            <button type="button" className="ghost" onClick={() => void copyIngestToken()}>{copied ? "Copied" : "Copy token"}</button>
            <button type="button" onClick={() => {
              setToken(setupResult.access_token);
              setSetupResult(null);
            }}>Continue to Fanout</button>
          </div>
        </section>
      </main>
    );
  }

  if (token && returnTo) return null;
  if (token) return <>{children}</>;

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (status?.setup_required) {
        const result = await jsonRequest("/api/auth/setup", { email, name, setup_token: setupToken }) as SetupResult;
        saveToken(result.access_token);
        if (result.ingest_token) setSetupResult(result);
        else setToken(result.access_token);
      } else if (!codeSent) {
        await jsonRequest("/api/auth/start", { email });
        setCodeSent(true);
      } else {
        const result = await jsonRequest("/api/auth/verify", { email, code });
        saveToken(result.access_token);
        setToken(result.access_token);
      }
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="auth-shell">
      <section className="auth-card">
        <div className="brand-mark">F</div>
        <p className="eyebrow">FANOUT</p>
        <h1>{status?.setup_required ? "Create the first admin" : "Sign in to investigate"}</h1>
        <p className="muted">
          {status?.setup_required
            ? "Use the one-time token printed by the Fanout process."
            : codeSent
              ? `Enter the verification code sent to ${email}.`
              : "Fanout sends a short verification code to your email."}
        </p>
        <form onSubmit={submit}>
          <label>Email<input type="email" required value={email} onChange={(event) => setEmail(event.target.value)} disabled={codeSent} /></label>
          {status?.setup_required && <label>Name<input value={name} onChange={(event) => setName(event.target.value)} /></label>}
          {status?.setup_required && <label>Setup token<input required value={setupToken} onChange={(event) => setSetupToken(event.target.value)} autoComplete="one-time-code" /></label>}
          {!status?.setup_required && codeSent && <label>Verification code<input required value={code} onChange={(event) => setCode(event.target.value)} autoComplete="one-time-code" /></label>}
          {error && <p className="error">{error}</p>}
          <button type="submit" disabled={busy || !status}>{busy ? "Working…" : status?.setup_required ? "Create admin" : codeSent ? "Verify" : "Send code"}</button>
        </form>
      </section>
    </main>
  );
}
