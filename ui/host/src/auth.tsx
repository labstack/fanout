import { Alert, Button, Center, Code, Container, Group, Loader, Paper, Stack, Text, TextInput, Title } from "@mantine/core";
import { ArrowRight, Check, Copy, UserPlus } from "@phosphor-icons/react";
import { FormEvent, ReactNode, useEffect, useState } from "react";
import { BrandMark } from "./brand";

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
    refreshPromise = fetch("/api/auth/refresh", { method: "POST", credentials: "same-origin" })
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
type SetupResult = { access_token: string; ingest_token?: string; ingest_header_name?: string; suggested_endpoint?: string };

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

function AuthSurface({ children, wide = false }: { children: ReactNode; wide?: boolean }) {
  return <Center mih="100dvh" p="md"><Container size={wide ? 620 : 440} w="100%"><Paper withBorder shadow="xl" radius="xl" p={{ base: "lg", sm: "xl" }}>{children}</Paper></Container></Center>;
}

export default function AuthGate({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [token, setToken] = useState(getToken());
  const [sessionReady, setSessionReady] = useState(!getToken());
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
    if (getToken()) {
      refreshAccessToken().then((accessToken) => setToken(accessToken)).catch(() => undefined).finally(() => setSessionReady(true));
    }
    const handleUnauthorized = () => setToken("");
    window.addEventListener(unauthorizedEvent, handleUnauthorized);
    return () => window.removeEventListener(unauthorizedEvent, handleUnauthorized);
  }, []);

  useEffect(() => {
    if (token && sessionReady && returnTo) window.location.replace(returnTo);
  }, [returnTo, sessionReady, token]);

  async function copyIngestToken() {
    try {
      await navigator.clipboard.writeText(setupResult?.ingest_token ?? "");
      setCopied(true);
    } catch {
      setError("Clipboard access failed. Select and copy the token manually.");
    }
  }

  if (setupResult?.ingest_token) {
    return <AuthSurface wide><Stack gap="lg">
      <BrandMark />
      <div><Text c="teal" fw={700} size="xs" tt="uppercase" lts="0.12em">Setup complete</Text><Title order={1} mt="xs">Save your ingest token</Title></div>
      <Text c="dimmed">Fanout shows this token once. Store it with your collector secrets before continuing.</Text>
      <Stack gap="xs"><Text size="sm" fw={600}>OTLP endpoint</Text><Code block>{setupResult.suggested_endpoint ?? "demo.fanout.test:4317"}</Code></Stack>
      <Stack gap="xs"><Text size="sm" fw={600}>Header</Text><Code block>{setupResult.ingest_header_name ?? "x-fanout-ingest-token"}: {setupResult.ingest_token}</Code></Stack>
      {error && <Alert color="red">{error}</Alert>}
      <Group grow align="stretch">
        <Button variant="default" leftSection={copied ? <Check size={16} weight="bold" /> : <Copy size={16} />} onClick={() => void copyIngestToken()}>{copied ? "Copied" : "Copy token"}</Button>
        <Button rightSection={<ArrowRight size={16} weight="bold" />} onClick={() => { setToken(setupResult.access_token); setSetupResult(null); }}>Continue to Fanout</Button>
      </Group>
    </Stack></AuthSurface>;
  }

  if (token && !sessionReady) return <Center mih="100dvh"><Loader size="sm" /></Center>;
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
        if (result.ingest_token) setSetupResult(result); else setToken(result.access_token);
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

  return <AuthSurface><Stack gap="lg">
    <BrandMark />
    <div><Text c="teal" fw={700} size="xs" tt="uppercase" lts="0.12em">Fanout</Text><Title order={1} mt="xs">{status?.setup_required ? "Create the first admin" : "Sign in to investigate"}</Title></div>
    <Text c="dimmed">
      {status?.setup_required ? "Use the one-time token printed by the Fanout process." : codeSent ? `Enter the verification code sent to ${email}.` : "Fanout sends a short verification code to your email."}
    </Text>
    <form onSubmit={submit}><Stack>
      <TextInput label="Email" type="email" required value={email} onChange={(event) => setEmail(event.currentTarget.value)} disabled={codeSent} />
      {status?.setup_required && <TextInput label="Name" value={name} onChange={(event) => setName(event.currentTarget.value)} />}
      {status?.setup_required && <TextInput label="Setup token" required value={setupToken} onChange={(event) => setSetupToken(event.currentTarget.value)} autoComplete="one-time-code" />}
      {!status?.setup_required && codeSent && <TextInput label="Verification code" required value={code} onChange={(event) => setCode(event.currentTarget.value)} autoComplete="one-time-code" />}
      {error && <Alert color="red">{error}</Alert>}
      <Button type="submit" size="md" loading={busy} disabled={!status} leftSection={status?.setup_required ? <UserPlus size={17} weight="bold" /> : undefined} rightSection={!status?.setup_required ? <ArrowRight size={17} weight="bold" /> : undefined}>{status?.setup_required ? "Create admin" : codeSent ? "Verify" : "Send code"}</Button>
    </Stack></form>
  </Stack></AuthSurface>;
}
