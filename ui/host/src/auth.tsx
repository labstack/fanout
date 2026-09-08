import { Alert, Box, Button, Center, Code, Container, Group, Loader, Paper, PinInput, Stack, Text, TextInput, Title } from "@mantine/core";
import { ArrowLeft, ArrowRight, Check, Copy, UserPlus } from "@phosphor-icons/react";
import { useNavigate } from "@tanstack/react-router";
import { createContext, FormEvent, ReactNode, useContext, useEffect, useRef, useState } from "react";
import { browserViewerFromMe, BrowserViewer, clearLegacySession, oauthReturnTo, unauthorizedEvent } from "./auth-session";
import { BrandLockup } from "./brand";

export { authorizedFetch, clearSession, logout } from "./auth-session";

export type Status = {
  setup_required: boolean;
  auth_mode: "local" | "oidc";
  agent_available: boolean;
  smtp_configured: boolean;
  self_signup: boolean;
};
type SetupResult = { status: string; ingest_token?: string; ingest_header_name?: string; suggested_endpoint?: string };
const RuntimeStatusContext = createContext<Status | null>(null);

export function useRuntimeStatus(): Status {
  const status = useContext(RuntimeStatusContext);
  if (!status) throw new Error("Fanout runtime status is unavailable");
  return status;
}

export type Viewer = { id: string; email: string; name: string; role: string };
const ViewerContext = createContext<Viewer | null>(null);

export function useViewer(): Viewer {
  const viewer = useContext(ViewerContext);
  if (!viewer) throw new Error("Fanout viewer is unavailable");
  return viewer;
}

function viewerFromMe(user: unknown): Viewer | null {
  if (!user || typeof user !== "object") return null;
  const record = user as Record<string, unknown>;
  if (typeof record.id !== "string" || record.id === "") return null;
  return {
    id: record.id,
    email: typeof record.email === "string" ? record.email : "",
    name: typeof record.name === "string" ? record.name : "",
    role: typeof record.role === "string" ? record.role : "",
  };
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
      // Tokens rather than literals: the old mint wash was left over from the
      // teal palette, and --mantine-color-white is white in both schemes, so
      // the sign-in page stayed light while the rest of the app went dark.
      background: "radial-gradient(circle at 50% -12%, var(--mantine-color-brand-light), transparent 38%), linear-gradient(180deg, var(--mantine-color-default-hover), var(--mantine-color-body) 62%)",
    }}
  >
    <Center mih="100dvh" px="md" py={48}>
      <Container size={wide ? 680 : 480} w="100%">
        <Paper
          radius={28}
          p={{ base: 24, sm: 40 }}
          style={{
            background: "var(--mantine-color-body)",
            border: "1px solid var(--mantine-color-default-border)",
            boxShadow: "var(--mantine-shadow-xl)",
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
  const [account, setAccount] = useState<Viewer | null>(null);
  const [sessionReady, setSessionReady] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [setupToken, setSetupToken] = useState(readSetupTokenFromURL);
  const [loginToken, setLoginToken] = useState(readLoginTokenFromURL);
  const [code, setCode] = useState("");
  const [codeSent, setCodeSent] = useState(false);
  const [resendAt, setResendAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [setupResult, setSetupResult] = useState<SetupResult | null>(null);
  const [copied, setCopied] = useState(false);
  const codeRef = useRef<HTMLDivElement>(null);
  const returnTo = oauthReturnTo();
  const authenticated = viewer === "user";

  async function loadAccount() {
    const response = await fetch("/api/auth/me", { credentials: "same-origin" });
    if (!response.ok) { setViewer("none"); setAccount(null); return; }
    const user = await response.json().catch(() => null);
    setViewer(browserViewerFromMe(user));
    setAccount(viewerFromMe(user));
  }

  // After a login the session is known to exist; a failed reload must not
  // undo it, so this only fills in the account.
  async function refreshAccount() {
    try {
      const response = await fetch("/api/auth/me", { credentials: "same-origin" });
      if (!response.ok) return;
      setAccount(viewerFromMe(await response.json().catch(() => null)));
    } catch {
      // The typed email stands in until the next boot.
    }
  }

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
    loadAccount().catch(() => setViewer("none")).finally(() => setSessionReady(true));
    // The session is gone, so the account that came with it is gone too:
    // leaving it behind would name the signed-out viewer on the next sign-in
    // screen and in whatever renders before the fresh account arrives.
    const handleUnauthorized = () => { setViewer("none"); setAccount(null); };
    window.addEventListener(unauthorizedEvent, handleUnauthorized);
    return () => window.removeEventListener(unauthorizedEvent, handleUnauthorized);
  }, []);

  useEffect(() => {
    if (!sessionReady || viewer !== "none" || !loginToken) return;
    setBusy(true);
    setError("");
    jsonRequest("/api/auth/login-link", { token: loginToken })
      .then(() => { setViewer("user"); void refreshAccount(); })
      .catch((value) => setError(value instanceof Error ? value.message : String(value)))
      .finally(() => { setLoginToken(""); setBusy(false); });
  }, [loginToken, sessionReady, viewer]);

  useEffect(() => {
    if (authenticated && sessionReady && returnTo) window.location.replace(returnTo);
  }, [authenticated, returnTo, sessionReady]);

  // The resend cooldown is a wall-clock deadline rather than a tick count, so a
  // backgrounded tab cannot stall it. The ticker exists only for the countdown:
  // it never starts without a live deadline and stops itself at the deadline, so
  // no interval outlives the code step.
  useEffect(() => {
    if (resendAt <= Date.now()) return;
    const timer = setInterval(() => {
      setNow(Date.now());
      if (Date.now() >= resendAt) clearInterval(timer);
    }, 1000);
    return () => clearInterval(timer);
  }, [resendAt]);
  const resendWait = Math.max(0, Math.ceil((resendAt - now) / 1000));

  // A rejected code empties the boxes, which leaves the caret in the last one
  // with nowhere to type. Focus goes back to the first digit once the request
  // has settled, because the boxes are disabled while it is in flight.
  useEffect(() => {
    if (!codeSent || busy || !error) return;
    codeRef.current?.querySelector("input")?.focus();
  }, [busy, codeSent, error]);

  async function copyText(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      setError("Clipboard access failed. Select and copy the token manually.");
    }
  }

  if (setupResult?.ingest_token) {
    return <AuthSurface wide><Stack gap="lg">
      <BrandLockup />
      <div><Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">Setup complete</Text><Title order={1} mt="xs" fz={{ base: 28, sm: 32 }} lh={1.08}>Save your ingest token</Title></div>
      <Text c="dimmed">Fanout shows this token once. Store it with your collector secrets before continuing.</Text>
      <Stack gap="xs"><Text size="sm" fw={600}>OTLP endpoint</Text><Code block>{setupResult.suggested_endpoint ?? window.location.origin}</Code></Stack>
      <Stack gap="xs"><Text size="sm" fw={600}>Header</Text><Code block>{setupResult.ingest_header_name ?? "Authorization"}: Bearer {setupResult.ingest_token}</Code></Stack>
      {error && <Alert color="bad" radius="md">{error}</Alert>}
      <Group grow align="stretch">
        <Button variant="light" radius="md" leftSection={copied ? <Check size={16} weight="bold" /> : <Copy size={16} />} onClick={() => void copyText(`${setupResult.ingest_header_name ?? "Authorization"}: Bearer ${setupResult.ingest_token}`)}>{copied ? "Copied" : "Copy header"}</Button>
        <Button radius="md" rightSection={<ArrowRight size={16} weight="bold" />} onClick={() => { setViewer("user"); setSetupResult(null); void refreshAccount(); }}>Continue to Fanout</Button>
      </Group>
      <Button variant="subtle" size="compact-xs" onClick={() => void copyText(setupResult.ingest_token ?? "")}>Copy token only</Button>
    </Stack></AuthSurface>;
  }

  if (!sessionReady || !statusReady || loginToken) return <Center mih="100dvh"><Loader size="sm" /></Center>;
  if (!status) return <AuthSurface><Stack gap="lg"><BrandLockup /><Title order={1}>Fanout is unavailable</Title><Alert color="bad" radius="md">{error || "Authentication status could not be loaded."}</Alert></Stack></AuthSurface>;
  if (authenticated && returnTo) return null;
  if (authenticated) return <RuntimeStatusContext.Provider value={status}>
    <ViewerContext.Provider value={account ?? { id: "", email, name, role: "" }}>{children}</ViewerContext.Provider>
  </RuntimeStatusContext.Provider>;

  if (status && !status.setup_required && status.auth_mode === "oidc") {
    const target = returnTo ? `/api/auth/oidc/start?return_to=${encodeURIComponent(returnTo)}` : "/api/auth/oidc/start";
    return <AuthSurface><Stack gap={28}>
      <BrandLockup />
      <Stack gap={10}>
        <Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">Secure workspace</Text>
        <Title order={1} fz={{ base: 28, sm: 32 }} lh={1.08}>Sign in</Title>
        <Text c="dimmed" size="md" lh={1.6}>Use your organization&apos;s identity provider to continue.</Text>
      </Stack>
      {error && <Alert color="bad" radius="md">{error}</Alert>}
      <Button component="a" href={target} size="md" radius="md" rightSection={<ArrowRight size={17} weight="bold" />}>Continue with SSO</Button>
    </Stack></AuthSurface>;
  }

  async function sendCode() {
    setBusy(true);
    setError("");
    try {
      await jsonRequest("/api/auth/start", { email });
      setCodeSent(true);
      setCode("");
      setResendAt(Date.now() + 30_000);
      setNow(Date.now());
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
    } finally {
      setBusy(false);
    }
  }

  async function verifyCode(value: string) {
    setBusy(true);
    setError("");
    try {
      await jsonRequest("/api/auth/verify", { email, code: value });
      setViewer("user");
      // A later re-authentication in the same tab starts on the email step
      // rather than remounting on a stale code step, and clearing the deadline
      // stops the countdown ticker now instead of when it would have expired.
      setCodeSent(false);
      setCode("");
      setResendAt(0);
      void refreshAccount();
    } catch (cause) {
      setCode("");
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }

  function changeEmail() {
    setCodeSent(false);
    setCode("");
    setError("");
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (status?.setup_required) {
        const result = await jsonRequest("/api/auth/setup", { email, name, setup_token: setupToken }) as SetupResult;
        if (result.ingest_token) setSetupResult(result); else { setViewer("user"); void refreshAccount(); }
      } else if (!codeSent) {
        await sendCode();
        return;
      } else {
        await verifyCode(code);
        return;
      }
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
    } finally {
      setBusy(false);
    }
  }

  // The code step is its own page: it says a code was sent above the fold
  // rather than in the smallest line on the screen, and it computes its copy
  // first so an empty description holds no space.
  const onCodeStep = codeSent && !status?.setup_required;
  const heading = status?.setup_required ? "Create the first admin" : onCodeStep ? "Check your email" : status?.self_signup ? "Sign in or create an account" : "Sign in";
  const description = status?.setup_required ? "Use the one-time token printed by the Fanout process."
    : !status?.smtp_configured ? "Email delivery is not configured. Ask the operator to run fanout login-link with your email address."
      : onCodeStep ? `We sent a code to ${email}.`
        : status?.self_signup ? "Enter your email to sign in or create a viewer account. No password needed."
          : "Enter your email and we’ll send a short verification code. No password needed.";

  return <AuthSurface><Stack gap={28}>
    <BrandLockup />
    <Stack gap={10}>
      <Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">
        {status?.setup_required ? "One-time setup" : "Secure workspace"}
      </Text>
      <Title order={1} fz={{ base: 28, sm: 32 }} lh={1.08}>{heading}</Title>
      {description && <Text c="dimmed" size="md" lh={1.6} maw={390}>{description}</Text>}
    </Stack>
    <form onSubmit={submit}><Stack gap="md">
      <TextInput label="Email" placeholder="you@company.com" type="email" required value={email} onChange={(event) => setEmail(event.currentTarget.value)} disabled={codeSent} variant="filled" radius="md" size="md" autoFocus={!codeSent} />
      {status?.setup_required && <TextInput label="Name" placeholder="Your name" value={name} onChange={(event) => setName(event.currentTarget.value)} variant="filled" radius="md" size="md" />}
      {status?.setup_required && <TextInput label="Setup token" placeholder="from the setup URL printed at startup" required value={setupToken} onChange={(event) => setSetupToken(event.currentTarget.value)} autoComplete="one-time-code" variant="filled" radius="md" size="md" />}
      {onCodeStep && <Stack gap="xs">
        <Text size="sm" fw={600}>Verification code</Text>
        <Box ref={codeRef}><PinInput length={6} type="number" oneTimeCode autoFocus value={code} onChange={setCode} onComplete={(value) => void verifyCode(value)} disabled={busy} error={Boolean(error)} size="md" radius="md" aria-label="Verification code" getInputProps={(index) => ({ "aria-label": `Digit ${index + 1}` })} /></Box>
        <Group justify="space-between" gap="xs">
          <Text c="dimmed" size="xs">Sent to {email}</Text>
          <Button variant="subtle" size="compact-xs" leftSection={<ArrowLeft size={12} weight="bold" />} onClick={changeEmail} disabled={busy}>Change email</Button>
        </Group>
        <Group justify="space-between" gap="xs">
          <Text c="dimmed" size="xs">Codes expire in 5 minutes</Text>
          <Button variant="subtle" size="compact-xs" onClick={() => void sendCode()} disabled={busy || resendWait > 0}>{resendWait > 0 ? `Resend in ${resendWait}s` : "Resend code"}</Button>
        </Group>
      </Stack>}
      {error && <Alert color="bad" radius="md">{error}</Alert>}
      {/* The sixth digit still submits on its own, but a partial paste, a short
          autofill or a cleared field has to leave something to press. */}
      {onCodeStep
        ? <Button type="submit" size="md" radius="md" mt={4} loading={busy} disabled={busy || code.length < 6} rightSection={<ArrowRight size={17} weight="bold" />}>Verify code</Button>
        : <Button type="submit" size="md" radius="md" mt={4} loading={busy} disabled={!status || (!status.setup_required && !status.smtp_configured)} leftSection={status?.setup_required ? <UserPlus size={17} weight="bold" /> : undefined} rightSection={!status?.setup_required ? <ArrowRight size={17} weight="bold" /> : undefined}>{status?.setup_required ? "Create admin" : "Send code"}</Button>}
    </Stack></form>
  </Stack></AuthSurface>;
}
