import { Alert, Box, Button, Center, Code, Container, Group, Loader, Paper, Stack, Text, TextInput, Title } from "@mantine/core";
import { ArrowRight, Check, Copy, UserPlus } from "@phosphor-icons/react";
import { useNavigate } from "@tanstack/react-router";
import { createContext, FormEvent, ReactNode, useContext, useEffect, useState } from "react";
import { browserViewerFromMe, BrowserViewer, clearLegacySession, oauthReturnTo, unauthorizedEvent } from "./auth-session";
import { BrandLockup } from "./brand";

export { authorizedFetch, clearSession, logout } from "./auth-session";

export type Status = {
  setup_required: boolean;
  auth_mode: "local" | "oidc";
  agent_available: boolean;
  smtp_configured: boolean;
};
type SetupResult = { status: string; ingest_token?: string; ingest_header_name?: string; suggested_endpoint?: string };
const RuntimeStatusContext = createContext<Status | null>(null);

export function useRuntimeStatus(): Status {
  const status = useContext(RuntimeStatusContext);
  if (!status) throw new Error("Fanout runtime status is unavailable");
  return status;
}

async function jsonRequest(path: string, body?: unknown) {
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.message ?? payload.error ?? `Request failed (${response.status})`);
  return payload;
}

function AuthSurface({ children, wide = false }: { children: ReactNode; wide?: boolean }) {
  return <Box
    mih="100dvh"
    style={{
      background: "radial-gradient(circle at 50% -12%, rgba(70, 192, 142, 0.12), transparent 38%), linear-gradient(180deg, var(--mantine-color-gray-0), var(--mantine-color-white) 62%)",
    }}
  >
    <Center mih="100dvh" px="md" py={48}>
      <Container size={wide ? 680 : 480} w="100%">
        <Paper
          radius={28}
          p={{ base: 24, sm: 40 }}
          style={{
            background: "rgba(255, 255, 255, 0.9)",
            border: "1px solid rgba(31, 41, 55, 0.08)",
            boxShadow: "0 28px 70px rgba(31, 41, 55, 0.10), 0 3px 10px rgba(31, 41, 55, 0.04)",
            backdropFilter: "blur(18px)",
          }}
        >
          {children}
        </Paper>
      </Container>
    </Center>
  </Box>;
}

// The setup credential is delivered in the URL printed at first boot. Reading
// it stays pure so a re-invoked state initializer cannot lose it; stripping it
// from the address bar happens in an effect, so it does not linger in history,
// screenshots, or a shared screen.
function readSetupTokenFromURL(): string {
  if (typeof window === "undefined") return "";
  return new URLSearchParams(window.location.search).get("setup_token") ?? "";
}

function readLoginTokenFromURL(): string {
  if (typeof window === "undefined") return "";
  return new URLSearchParams(window.location.search).get("login_token") ?? "";
}

export default function AuthGate({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const [status, setStatus] = useState<Status | null>(null);
  const [statusReady, setStatusReady] = useState(false);
  const [viewer, setViewer] = useState<BrowserViewer>("none");
  const [sessionReady, setSessionReady] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [setupToken, setSetupToken] = useState(readSetupTokenFromURL);
  const [loginToken, setLoginToken] = useState(readLoginTokenFromURL);
  const [code, setCode] = useState("");
  const [codeSent, setCodeSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [setupResult, setSetupResult] = useState<SetupResult | null>(null);
  const [copied, setCopied] = useState(false);
  const returnTo = oauthReturnTo();
  const authenticated = viewer === "user";

  useEffect(() => {
    const url = new URL(window.location.href);
    if (!url.searchParams.has("setup_token") && !url.searchParams.has("login_token")) return;
    url.searchParams.delete("setup_token");
    url.searchParams.delete("login_token");
    void navigate({ href: url.pathname + url.search + url.hash, replace: true });
  }, [navigate]);

  useEffect(() => {
    clearLegacySession();
    jsonRequest("/api/auth/status").then(setStatus).catch((value) => setError(String(value))).finally(() => setStatusReady(true));
    fetch("/api/auth/me", { credentials: "same-origin" })
      .then(async (response) => {
        if (!response.ok) {
          setViewer("none");
          return;
        }
        const user = await response.json().catch(() => null);
        setViewer(browserViewerFromMe(user));
      })
      .catch(() => setViewer("none"))
      .finally(() => setSessionReady(true));
    const handleUnauthorized = () => setViewer("none");
    window.addEventListener(unauthorizedEvent, handleUnauthorized);
    return () => window.removeEventListener(unauthorizedEvent, handleUnauthorized);
  }, []);

  useEffect(() => {
    if (!sessionReady || viewer !== "none" || !loginToken) return;
    setBusy(true);
    setError("");
    jsonRequest("/api/auth/login-link", { token: loginToken })
      .then(() => setViewer("user"))
      .catch((value) => setError(value instanceof Error ? value.message : String(value)))
      .finally(() => { setLoginToken(""); setBusy(false); });
  }, [loginToken, sessionReady, viewer]);

  useEffect(() => {
    if (authenticated && sessionReady && returnTo) window.location.replace(returnTo);
  }, [authenticated, returnTo, sessionReady]);

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
      <BrandLockup />
      <div><Text c="teal" fw={700} size="xs" tt="uppercase" lts="0.12em">Setup complete</Text><Title order={1} mt="xs" fz={{ base: 30, sm: 36 }} fw={650} lh={1.08}>Save your ingest token</Title></div>
      <Text c="dimmed">Fanout shows this token once. Store it with your collector secrets before continuing.</Text>
      <Stack gap="xs"><Text size="sm" fw={600}>OTLP endpoint</Text><Code block>{setupResult.suggested_endpoint ?? `${window.location.hostname}:4317`}</Code></Stack>
      <Stack gap="xs"><Text size="sm" fw={600}>Header</Text><Code block>{setupResult.ingest_header_name ?? "Authorization"}: Bearer {setupResult.ingest_token}</Code></Stack>
      {error && <Alert color="red" radius="md">{error}</Alert>}
      <Group grow align="stretch">
        <Button variant="light" radius="md" leftSection={copied ? <Check size={16} weight="bold" /> : <Copy size={16} />} onClick={() => void copyIngestToken()}>{copied ? "Copied" : "Copy token"}</Button>
        <Button radius="md" rightSection={<ArrowRight size={16} weight="bold" />} onClick={() => { setViewer("user"); setSetupResult(null); }}>Continue to Fanout</Button>
      </Group>
    </Stack></AuthSurface>;
  }

  if (!sessionReady || !statusReady || loginToken) return <Center mih="100dvh"><Loader size="sm" /></Center>;
  if (!status) return <AuthSurface><Stack gap="lg"><BrandLockup /><Title order={1}>Fanout is unavailable</Title><Alert color="red" radius="md">{error || "Authentication status could not be loaded."}</Alert></Stack></AuthSurface>;
  if (authenticated && returnTo) return null;
  if (authenticated) return <RuntimeStatusContext.Provider value={status}>{children}</RuntimeStatusContext.Provider>;

  if (status && !status.setup_required && status.auth_mode === "oidc") {
    const target = returnTo ? `/api/auth/oidc/start?return_to=${encodeURIComponent(returnTo)}` : "/api/auth/oidc/start";
    return <AuthSurface><Stack gap={28}>
      <BrandLockup />
      <Stack gap={10}>
        <Text c="teal.7" fw={700} size="xs" tt="uppercase" lts="0.12em">Secure workspace</Text>
        <Title order={1} fz={{ base: 30, sm: 36 }} fw={650} lh={1.08}>Sign in to investigate</Title>
        <Text c="dimmed" size="md" lh={1.6}>Use your organization&apos;s identity provider to continue.</Text>
      </Stack>
      {error && <Alert color="red" radius="md">{error}</Alert>}
      <Button component="a" href={target} size="md" radius="md" rightSection={<ArrowRight size={17} weight="bold" />}>Continue with SSO</Button>
    </Stack></AuthSurface>;
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (status?.setup_required) {
        const result = await jsonRequest("/api/auth/setup", { email, name, setup_token: setupToken }) as SetupResult;
        if (result.ingest_token) setSetupResult(result); else setViewer("user");
      } else if (!codeSent) {
        await jsonRequest("/api/auth/start", { email });
        setCodeSent(true);
      } else {
        await jsonRequest("/api/auth/verify", { email, code });
        setViewer("user");
      }
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
    } finally {
      setBusy(false);
    }
  }

  return <AuthSurface><Stack gap={28}>
    <BrandLockup />
    <Stack gap={10}>
      <Text c="teal.7" fw={700} size="xs" tt="uppercase" lts="0.12em">
        {status?.setup_required ? "One-time setup" : "Secure workspace"}
      </Text>
      <Title order={1} fz={{ base: 30, sm: 36 }} fw={650} lh={1.08}>
        {status?.setup_required ? "Create the first admin" : "Sign in to investigate"}
      </Title>
      <Text c="dimmed" size="md" lh={1.6} maw={390}>
        {status?.setup_required ? "Use the one-time token printed by the Fanout process." : !status?.smtp_configured ? "Email delivery is not configured. Ask the operator to run fanout login-link with your email address." : codeSent ? `Enter the verification code sent to ${email}.` : "Enter your email and we’ll send a short verification code. No password needed."}
      </Text>
    </Stack>
    <form onSubmit={submit}><Stack gap="md">
      <TextInput label="Email" placeholder="you@company.com" type="email" required value={email} onChange={(event) => setEmail(event.currentTarget.value)} disabled={codeSent} variant="filled" radius="md" size="md" autoFocus={!codeSent} />
      {status?.setup_required && <TextInput label="Name" placeholder="Your name" value={name} onChange={(event) => setName(event.currentTarget.value)} variant="filled" radius="md" size="md" />}
      {status?.setup_required && <TextInput label="Setup token" placeholder="from the setup URL printed at startup" required value={setupToken} onChange={(event) => setSetupToken(event.currentTarget.value)} autoComplete="one-time-code" variant="filled" radius="md" size="md" />}
      {!status?.setup_required && codeSent && <TextInput label="Verification code" placeholder="000000" required value={code} onChange={(event) => setCode(event.currentTarget.value)} autoComplete="one-time-code" variant="filled" radius="md" size="md" styles={{ input: { letterSpacing: "0.2em", fontVariantNumeric: "tabular-nums" } }} autoFocus />}
      {error && <Alert color="red" radius="md">{error}</Alert>}
      <Button type="submit" size="md" radius="md" mt={4} loading={busy} disabled={!status || (!status.setup_required && !status.smtp_configured)} leftSection={status?.setup_required ? <UserPlus size={17} weight="bold" /> : undefined} rightSection={!status?.setup_required ? <ArrowRight size={17} weight="bold" /> : undefined}>{status?.setup_required ? "Create admin" : codeSent ? "Verify code" : "Send code"}</Button>
    </Stack></form>
  </Stack></AuthSurface>;
}
