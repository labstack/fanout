import { AuthFrame, DataState, EmailCodeSignIn, Notice, PageHeader, type SignInPending } from "@labstack/ui";
import { Anchor, Button, Code, Group, Stack, Text, TextInput } from "@mantine/core";
import { ArrowRight, Check, Copy, UserPlus } from "@phosphor-icons/react";
import { useNavigate } from "@tanstack/react-router";
import { createContext, FormEvent, ReactNode, useContext, useEffect, useRef, useState } from "react";
import { browserViewerFromMe, BrowserViewer, oauthReturnTo, unauthorizedEvent } from "./auth-session";
import { BrandMark } from "./brand";
import { docs } from "../../links";

export { authorizedFetch, logout } from "./auth-session";

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

export type Viewer = { id: string; email: string; display_name: string; status: "active" | "suspended"; role: string };
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
  if (record.status !== "active" && record.status !== "suspended") return null;
  return {
    id: record.id,
    email: typeof record.email === "string" ? record.email : "",
    display_name: typeof record.display_name === "string" ? record.display_name : "",
    status: record.status,
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
  if (!response.ok) throw new Error(payload.message ?? `Request failed (${response.status})`);
  return payload;
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
  const [displayName, setDisplayName] = useState("");
  const [setupToken, setSetupToken] = useState(readSetupTokenFromURL);
  const [loginToken, setLoginToken] = useState(readLoginTokenFromURL);
  const [code, setCode] = useState("");
  const [codeSent, setCodeSent] = useState(false);
  const [resendAt, setResendAt] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [signInPending, setSignInPending] = useState<SignInPending>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [setupResult, setSetupResult] = useState<SetupResult | null>(null);
  const [copied, setCopied] = useState(false);
  const emailInput = useRef<HTMLInputElement>(null);
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
    jsonRequest("/api/auth/link/verify", { token: loginToken })
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

  async function copyText(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      setError("Clipboard access failed. Select and copy the token manually.");
    }
  }

  if (setupResult?.ingest_token) {
    return <AuthFrame surface="plain" brand={<BrandMark />} footer={<Anchor href={docs.home} size="xs" target="_blank" rel="noopener noreferrer">Docs</Anchor>} size="lg"><Stack gap="lg">
      <PageHeader eyebrow="Setup complete" title="Save your ingest token" />
      <Text c="dimmed">Fanout shows this token once. Store it with your collector secrets before continuing.</Text>
      <Stack gap="xs"><Text size="sm" fw={600}>OTLP endpoint</Text><Code block>{setupResult.suggested_endpoint ?? window.location.origin}</Code></Stack>
      <Stack gap="xs"><Text size="sm" fw={600}>Header</Text><Code block>{setupResult.ingest_header_name ?? "Authorization"}: Bearer {setupResult.ingest_token}</Code></Stack>
      {error && <Notice tone="danger">{error}</Notice>}
      <Group grow align="stretch">
        <Button variant="light" radius="md" leftSection={copied ? <Check size={16} weight="bold" /> : <Copy size={16} />} onClick={() => void copyText(`${setupResult.ingest_header_name ?? "Authorization"}: Bearer ${setupResult.ingest_token}`)}>{copied ? "Copied" : "Copy header"}</Button>
        <Button radius="md" rightSection={<ArrowRight size={16} weight="bold" />} onClick={() => { setViewer("user"); setSetupResult(null); void refreshAccount(); }}>Continue to Fanout</Button>
      </Group>
      <Button variant="subtle" size="compact-xs" onClick={() => void copyText(setupResult.ingest_token ?? "")}>Copy token only</Button>
    </Stack></AuthFrame>;
  }

  if (!sessionReady || !statusReady || loginToken) return <AuthFrame surface="plain" brand={<BrandMark />} footer={<Anchor href={docs.home} size="xs" target="_blank" rel="noopener noreferrer">Docs</Anchor>}><DataState state="loading" loadingLabel="Loading sign-in" rows={4} /></AuthFrame>;
  if (!status) return <AuthFrame surface="plain" brand={<BrandMark />} footer={<Anchor href={docs.home} size="xs" target="_blank" rel="noopener noreferrer">Docs</Anchor>}><Stack gap="lg"><PageHeader title="Fanout is unavailable" /><Notice tone="danger">{error || "Authentication status could not be loaded."}</Notice></Stack></AuthFrame>;
  if (authenticated && returnTo) return null;
  if (authenticated) return <RuntimeStatusContext.Provider value={status}>
    <ViewerContext.Provider value={account ?? { id: "", email, display_name: displayName, status: "active", role: "" }}>{children}</ViewerContext.Provider>
  </RuntimeStatusContext.Provider>;

  if (status && !status.setup_required && status.auth_mode === "oidc") {
    const target = returnTo ? `/api/auth/oidc/start?return_to=${encodeURIComponent(returnTo)}` : "/api/auth/oidc/start";
    return <AuthFrame surface="plain" brand={<BrandMark />} footer={<Anchor href={docs.home} size="xs" target="_blank" rel="noopener noreferrer">Docs</Anchor>}><Stack gap={28}>
      <PageHeader title="Sign in to Fanout" description="Use your organization’s identity provider to continue." />
      {error && <Notice tone="danger">{error}</Notice>}
      <Button component="a" href={target} size="md" radius="md" rightSection={<ArrowRight size={17} weight="bold" />}>Continue with SSO</Button>
    </Stack></AuthFrame>;
  }

  async function sendCode() {
    if (busy || !status?.smtp_configured || (codeSent && resendWait > 0)) return;
    setBusy(true);
    setSignInPending(codeSent ? "resend" : "send");
    setError("");
    try {
      await jsonRequest("/api/auth/code/send", { email });
      setCodeSent(true);
      setCode("");
      setResendAt(Date.now() + 30_000);
      setNow(Date.now());
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
    } finally {
      setBusy(false);
      setSignInPending(null);
    }
  }

  async function verifyCode(value: string) {
    if (busy || value.length !== 6) return;
    setBusy(true);
    setSignInPending("verify");
    setError("");
    try {
      await jsonRequest("/api/auth/code/verify", { email, code: value });
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
      setSignInPending(null);
    }
  }

  function changeEmail() {
    setCodeSent(false);
    setCode("");
    setResendAt(0);
    setError("");
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await jsonRequest("/api/auth/setup", { email, display_name: displayName, setup_token: setupToken }) as SetupResult;
      if (result.ingest_token) setSetupResult(result); else { setViewer("user"); void refreshAccount(); }
    } catch (value) {
      setError(value instanceof Error ? value.message : String(value));
    } finally {
      setBusy(false);
    }
  }

  if (!status.setup_required) return <AuthFrame surface="plain" brand={<BrandMark />} footer={<Anchor href={docs.home} size="xs" target="_blank" rel="noopener noreferrer">Docs</Anchor>}>
    <fieldset disabled={!status.smtp_configured} style={{ border: 0, padding: 0, margin: 0, minWidth: 0 }}>
      <EmailCodeSignIn
        step={codeSent ? "code" : "email"}
        title="Sign in to Fanout"
        description={!status.smtp_configured ? "Email delivery is not configured. Ask the operator to run fanout login-link with your email address." : status.self_signup ? "We'll email you a 6-digit code. New here? This creates your account." : "We'll email you a 6-digit code."}
        email={email} onEmailChange={setEmail} inputRef={emailInput} onSendCode={() => { if (emailInput.current?.reportValidity()) void sendCode(); }}
        code={code} onCodeChange={setCode} onVerify={value => void verifyCode(value)}
        onChangeEmail={changeEmail} onResend={() => void sendCode()} resendIn={resendWait}
        pending={signInPending} error={error || undefined}
        stepLabels={false} expiresInMinutes={5}
        codeNote={<Text size="xs" c="dimmed">Codes expire in 5 minutes. Check your spam folder if the email hasn’t arrived.</Text>}
        labels={{ continue: "Continue with email", verify: "Verify code", resend: "Resend code", changeEmail: "Change email", codeSent: () => <>Enter the code sent to <Text span inherit fw={700}>{email}</Text>.</>, resendIn: countdown => `Resend in ${countdown}` }}
      />
    </fieldset>
  </AuthFrame>;

  return <AuthFrame surface="plain" brand={<BrandMark />} footer={<Anchor href={docs.home} size="xs" target="_blank" rel="noopener noreferrer">Docs</Anchor>}><Stack gap={28}>
    <PageHeader eyebrow="One-time setup" title="Create the first admin" description="Use the one-time token printed by the Fanout process." />
    <form onSubmit={submit}><Stack gap="md">
      <TextInput label="Email" placeholder="you@company.com" type="email" required value={email} onChange={event => setEmail(event.currentTarget.value)} autoFocus />
      <TextInput label="Display name" placeholder="Your display name" value={displayName} onChange={event => setDisplayName(event.currentTarget.value)} />
      <TextInput label="Setup token" placeholder="from the setup URL printed at startup" required value={setupToken} onChange={event => setSetupToken(event.currentTarget.value)} autoComplete="one-time-code" />
      {error && <Notice tone="danger">{error}</Notice>}
      <Button type="submit" loading={busy} leftSection={<UserPlus size={17} weight="bold" />}>Create admin</Button>
    </Stack></form>
  </Stack></AuthFrame>;
}
