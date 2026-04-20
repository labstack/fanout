import { useEffect, useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import { Check, Copy, Loader2, Radio } from "lucide-react";
import { setApiToken } from "@/api/client";
import { useAuth } from "@/hooks/auth-context";

type Step = "loading" | "setup" | "token_shown" | "email" | "code";

export function LoginPage() {
  const { user, isLoading, login } = useAuth();
  const [searchParams] = useSearchParams();
  const next = searchParams.get("next") || "/";

  const [step, setStep] = useState<Step>("loading");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);

  const [accessToken, setAccessToken] = useState<string | null>(null);
  const [ingestToken, setIngestToken] = useState<string | null>(null);
  const [ingestHeaderName, setIngestHeaderName] = useState("x-fanout-ingest-token");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const status = await fetch("/api/auth/status").then((r) => r.json());
        setStep(status.setup_required ? "setup" : "email");
      } catch {
        setStep("email");
      }
    })();
  }, []);

  if (!isLoading && user) {
    return <Navigate to={next} replace />;
  }

  async function handleSetup(e: React.FormEvent) {
    e.preventDefault();
    if (!email.trim()) return;
    setSending(true);
    setError(null);

    try {
      const result = await fetch("/api/auth/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          email: email.trim().toLowerCase(),
          name: name.trim(),
          setup_token: setupToken.trim(),
        }),
      });

      if (!result.ok) {
        const body = await result.json().catch(() => ({ detail: "Setup failed" }));
        setError(body.detail || body.message || "Setup failed");
        setSending(false);
        return;
      }

      const data = await result.json();
      setApiToken(data.access_token);
      setAccessToken(data.access_token);
      setIngestToken(data.ingest_token || null);
      if (data.ingest_header_name) setIngestHeaderName(data.ingest_header_name);
      setStep("token_shown");
      setSending(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Setup failed");
      setSending(false);
    }
  }

  async function continueAfterTokenShown() {
    if (!accessToken) return;
    await login(accessToken);
  }

  async function copyIngestConfig() {
    if (!ingestToken) return;
    const cfg = `OTEL_EXPORTER_OTLP_ENDPOINT=<host>:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_EXPORTER_OTLP_HEADERS=${ingestHeaderName}=${ingestToken}`;
    try {
      await navigator.clipboard.writeText(cfg);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API can be blocked in some contexts; silent fallback.
    }
  }

  async function handleEmailSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!email.trim()) return;
    setSending(true);
    setError(null);

    try {
      const res = await fetch("/api/auth/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: email.trim().toLowerCase() }),
      });
      const body = await res.json().catch(() => ({}));
      if (res.ok && body.code_sent) {
        setStep("code");
      } else {
        setError(body.detail || body.message || "Unable to send verification code.");
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to send code");
    } finally {
      setSending(false);
    }
  }

  async function handleCodeSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (code.length !== 6) return;
    setSending(true);
    setError(null);

    try {
      const result = await fetch("/api/auth/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ email: email.trim().toLowerCase(), code }),
      });

      if (!result.ok) {
        const body = await result.json().catch(() => ({ detail: "Invalid code" }));
        setError(body.detail || body.message || "Invalid or expired code");
        setSending(false);
        return;
      }

      const data = await result.json();
      setApiToken(data.access_token);
      await login(data.access_token);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Verification failed");
      setSending(false);
    }
  }

  if (step === "loading") {
    return (
      <div className="flex items-center justify-center min-h-screen bg-surface">
        <Loader2 className="h-6 w-6 text-primary animate-spin" />
      </div>
    );
  }

  return (
    <div className="flex items-center justify-center min-h-screen bg-surface">
      <div className="w-full max-w-md space-y-8 px-6">
        <div className="text-center space-y-3">
          <div className="inline-flex items-center justify-center">
            <Radio className="h-8 w-8 text-primary" />
          </div>
          <h1 className="font-heading text-2xl font-bold text-foreground">Fanout</h1>
          <p className="text-sm text-muted-foreground">
            {step === "setup" && "Create your admin account with the setup token shown in the server output"}
            {step === "token_shown" && "Save your ingest token — it won't be shown again"}
            {step === "email" && "Sign in to your account"}
            {step === "code" && `Enter the code sent to ${email}`}
          </p>
        </div>

        {error && (
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-sm text-unhealthy/90 mono">
            {error}
          </div>
        )}

        {step === "setup" && (
          <form onSubmit={handleSetup} className="space-y-4">
            <div>
              <label className="detail-label mb-2 block">Email</label>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="admin@company.com"
                className="input-field"
                autoFocus
                autoComplete="email"
                required
              />
            </div>
            <div>
              <label className="detail-label mb-2 block">Name (optional)</label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Your name"
                className="input-field"
              />
            </div>
            <div>
              <label className="detail-label mb-2 block">Setup token</label>
              <input
                type="password"
                value={setupToken}
                onChange={(e) => setSetupToken(e.target.value)}
                placeholder="Paste the setup token from the server output"
                className="input-field"
                autoComplete="one-time-code"
                required
              />
            </div>
            <p className="text-xs text-muted-foreground">
              Fanout prints this token in the server output while no admin exists. It stays valid until it expires or the first admin is created.
            </p>
            <button
              type="submit"
              disabled={sending || !email.trim() || !setupToken.trim()}
              className="btn-primary w-full disabled:opacity-50"
            >
              {sending ? (
                <span className="flex items-center justify-center gap-2">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Setting up...
                </span>
              ) : (
                "Create Admin Account"
              )}
            </button>
          </form>
        )}

        {step === "token_shown" && ingestToken && (
          <div className="space-y-4">
            <div className="rounded-lg border border-border bg-surface-2 px-4 py-4 space-y-3">
              <div className="flex items-center justify-between">
                <div className="detail-label">Collector configuration</div>
                <button
                  type="button"
                  onClick={copyIngestConfig}
                  className="flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground transition-colors mono"
                >
                  {copied ? <Check className="h-3.5 w-3.5 text-healthy" /> : <Copy className="h-3.5 w-3.5" />}
                  {copied ? "Copied" : "Copy"}
                </button>
              </div>
              <pre className="text-xs mono whitespace-pre-wrap text-foreground/80 break-all leading-6">
{`OTEL_EXPORTER_OTLP_ENDPOINT=<host>:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_EXPORTER_OTLP_HEADERS=${ingestHeaderName}=`}<span className="text-primary">{ingestToken}</span>
              </pre>
            </div>
            <p className="text-xs text-muted-foreground">
              This token won't be shown again. If you lose it, rotate a new one from the settings page.
            </p>
            <button
              type="button"
              onClick={() => void continueAfterTokenShown()}
              className="btn-primary w-full"
            >
              Continue to Fanout
            </button>
          </div>
        )}

        {step === "email" && (
          <form onSubmit={handleEmailSubmit} className="space-y-4">
            <div>
              <label className="detail-label mb-2 block">Email</label>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@company.com"
                className="input-field"
                autoFocus
                autoComplete="email"
                required
              />
            </div>
            <button
              type="submit"
              disabled={sending || !email.trim()}
              className="btn-primary w-full disabled:opacity-50"
            >
              {sending ? (
                <span className="flex items-center justify-center gap-2">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Sending code...
                </span>
              ) : (
                "Continue"
              )}
            </button>
          </form>
        )}

        {step === "code" && (
          <form onSubmit={handleCodeSubmit} className="space-y-4">
            <div>
              <label className="detail-label mb-2 block">Verification code</label>
              <input
                type="text"
                inputMode="numeric"
                pattern="[0-9]*"
                maxLength={6}
                value={code}
                onChange={(e) => {
                  const v = e.target.value.replace(/\D/g, "").slice(0, 6);
                  setCode(v);
                }}
                placeholder="000000"
                className="input-field text-center text-2xl tracking-[0.5em] mono"
                autoFocus
                autoComplete="one-time-code"
              />
            </div>
            <button
              type="submit"
              disabled={sending || code.length !== 6}
              className="btn-primary w-full disabled:opacity-50"
            >
              {sending ? (
                <span className="flex items-center justify-center gap-2">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Verifying...
                </span>
              ) : (
                "Verify"
              )}
            </button>
            <button
              type="button"
              onClick={() => {
                setStep("email");
                setCode("");
                setError(null);
              }}
              className="w-full text-xs text-muted-foreground hover:text-foreground transition-colors mono"
            >
              Use a different email
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
