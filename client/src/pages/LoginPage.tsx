import { useEffect, useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import { Loader2, Radio } from "lucide-react";
import { api, setApiToken } from "@/api/client";
import { useAuth } from "@/hooks/auth-context";

type Step = "loading" | "setup" | "ingest" | "email" | "code";

interface IngestConfigResponse {
  mode: string;
  public_endpoint: string;
  suggested_endpoint: string;
  tls_configured: boolean;
  header_name: string;
  ingest_token?: string;
}

export function LoginPage() {
  const { user, isLoading, login } = useAuth();
  const [searchParams] = useSearchParams();
  const next = searchParams.get("next") || "/";

  const [step, setStep] = useState<Step>("loading");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [code, setCode] = useState("");
  const [setupAccessToken, setSetupAccessToken] = useState<string | null>(null);
  const [publicEndpoint, setPublicEndpoint] = useState("");
  const [tlsConfigured, setTLSConfigured] = useState(false);
  const [headerName, setHeaderName] = useState("x-fanout-ingest-token");
  const [ingestToken, setIngestToken] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);

  // Check if setup is needed
  useEffect(() => {
    (async () => {
      try {
        const status = await fetch("/api/auth/status").then((r) => r.json());
        if (status.setup_required) {
          setStep("setup");
        } else {
          setStep("email");
        }
      } catch {
        setStep("email");
      }
    })();
  }, []);

  // Redirect if already logged in
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
      setSetupAccessToken(data.access_token);

      const ingest = await api<IngestConfigResponse>("/api/config/ingest");
      setPublicEndpoint(ingest.public_endpoint || ingest.suggested_endpoint);
      setTLSConfigured(ingest.tls_configured);
      setHeaderName(ingest.header_name);
      setStep("ingest");
      setSending(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Setup failed");
      setSending(false);
    }
  }

  async function completeSetup() {
    if (!setupAccessToken) return;
    await login(setupAccessToken);
  }

  async function handlePrivateIngest() {
    setSending(true);
    setError(null);

    try {
      await api<IngestConfigResponse>("/api/config/ingest", {
        method: "POST",
        body: JSON.stringify({ mode: "private" }),
      });
      await completeSetup();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to keep ingest private");
      setSending(false);
    }
  }

  async function handlePublicIngest(e: React.FormEvent) {
    e.preventDefault();
    setSending(true);
    setError(null);

    try {
      const result = await api<IngestConfigResponse>("/api/config/ingest", {
        method: "POST",
        body: JSON.stringify({
          mode: "public",
          public_endpoint: publicEndpoint.trim(),
        }),
      });
      setIngestToken(result.ingest_token || null);
      setPublicEndpoint(result.public_endpoint || publicEndpoint.trim());
      setHeaderName(result.header_name);
      setSending(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to enable public ingest");
      setSending(false);
    }
  }

  async function handleEmailSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!email.trim()) return;
    setSending(true);
    setError(null);

    try {
      const result = await api<{ code_sent: boolean }>("/api/auth/start", {
        method: "POST",
        body: JSON.stringify({ email: email.trim().toLowerCase() }),
      });
      if (result.code_sent) {
        setStep("code");
      } else {
        setError("Unable to send verification code.");
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
      <div className="w-full max-w-sm space-y-8 px-6">
        {/* Logo */}
        <div className="text-center space-y-3">
          <div className="inline-flex items-center justify-center">
            <Radio className="h-8 w-8 text-primary" />
          </div>
          <h1 className="font-heading text-2xl font-bold text-foreground">
            Fanout
          </h1>
          <p className="text-sm text-muted-foreground">
            {step === "setup" && "Create your admin account with the setup token shown in the server output"}
            {step === "ingest" && "Choose how collectors reach this Fanout instance"}
            {step === "email" && "Sign in to your account"}
            {step === "code" && `Enter the code sent to ${email}`}
          </p>
        </div>

        {/* Error */}
        {error && (
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-sm text-unhealthy/90 mono">
            {error}
          </div>
        )}

        {/* Setup — first boot, no email code needed */}
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

        {step === "ingest" && (
          <div className="space-y-4">
            {ingestToken ? (
              <>
                <div className="rounded-lg border border-border bg-surface-2 px-4 py-3 space-y-3">
                  <div className="detail-label">Public OTLP enabled</div>
                  <div className="space-y-2 text-sm mono text-foreground/80 break-all">
                    <div>
                      <span className="text-muted-foreground">Endpoint</span>
                      <span className="text-primary">={publicEndpoint}</span>
                    </div>
                    <div>
                      <span className="text-muted-foreground">{headerName}</span>
                      <span className="text-primary">={ingestToken}</span>
                    </div>
                  </div>
                </div>
                <div className="rounded-lg border border-border bg-surface-2 px-4 py-3 space-y-2">
                  <div className="detail-label">Collector config</div>
                  <pre className="text-xs mono whitespace-pre-wrap text-foreground/80">{`exporters:
  otlp:
    endpoint: ${publicEndpoint}
    tls:
      insecure: false
    headers:
      ${headerName}: ${ingestToken}`}</pre>
                </div>
                <button
                  type="button"
                  onClick={() => void completeSetup()}
                  className="btn-primary w-full"
                >
                  Continue to Fanout
                </button>
              </>
            ) : (
              <>
                <button
                  type="button"
                  onClick={() => void handlePrivateIngest()}
                  disabled={sending}
                  className="btn-primary w-full disabled:opacity-50"
                >
                  {sending ? "Saving..." : "Keep OTLP Private"}
                </button>

                <form onSubmit={handlePublicIngest} className="space-y-4 rounded-lg border border-border bg-surface-2 px-4 py-4">
                  <div className="space-y-2">
                    <div className="detail-label">Expose public OTLP</div>
                    <p className="text-xs text-muted-foreground">
                      Public ingest requires server TLS and a dedicated ingest token.
                    </p>
                  </div>
                  <div>
                    <label className="detail-label mb-2 block">Public endpoint</label>
                    <input
                      type="text"
                      value={publicEndpoint}
                      onChange={(e) => setPublicEndpoint(e.target.value)}
                      placeholder="fanout.example.com:4317"
                      className="input-field mono"
                      disabled={!tlsConfigured}
                      required
                    />
                  </div>
                  {!tlsConfigured && (
                    <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-xs text-unhealthy/90 mono">
                      Configure `TLS_CERT_FILE` and `TLS_KEY_FILE` before enabling public ingest.
                    </div>
                  )}
                  <button
                    type="submit"
                    disabled={sending || !tlsConfigured || !publicEndpoint.trim()}
                    className="btn-primary w-full disabled:opacity-50"
                  >
                    {sending ? "Enabling..." : "Enable Public OTLP"}
                  </button>
                </form>
              </>
            )}
          </div>
        )}

        {/* Email step */}
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

        {/* Code step */}
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
