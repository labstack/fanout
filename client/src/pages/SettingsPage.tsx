import { useState } from "react";
import { Check, Copy, ShieldCheck } from "lucide-react";
import { api } from "@/api/client";
import { useAuth } from "@/hooks/auth-context";
import { Navigate } from "react-router";

interface RotateResponse {
  ingest_token: string;
}

export function SettingsPage() {
  const { isAdmin, isLoading: authLoading } = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [revealedToken, setRevealedToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  if (authLoading) return null;
  if (!isAdmin) return <Navigate to="/" replace />;

  async function rotate() {
    if (!confirm("Generate a new ingest token? The previous token will stop working.")) return;
    setBusy(true);
    setError(null);
    try {
      const r = await api<RotateResponse>("/api/settings/ingest/rotate-token", { method: "POST" });
      setRevealedToken(r.ingest_token || null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to rotate token");
    } finally {
      setBusy(false);
    }
  }

  async function copyToken() {
    if (!revealedToken) return;
    try {
      await navigator.clipboard.writeText(revealedToken);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-3xl space-y-6 fade-up">
        <div>
          <h1 className="font-heading text-xl font-bold text-foreground">Settings</h1>
          <p className="text-sm text-muted-foreground mt-1">Admin-only configuration.</p>
        </div>

        {error && (
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-sm text-unhealthy/90 mono">
            {error}
          </div>
        )}

        <div className="stat-card space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-sm font-semibold text-foreground">Ingest token</h2>
              <p className="text-xs text-muted-foreground mt-0.5">
                Controls whether OTLP collectors must present a token on every request.
              </p>
            </div>
            <span className="flex items-center gap-1.5 text-[11px] text-healthy mono">
              <ShieldCheck className="h-3.5 w-3.5" />
              Required
            </span>
          </div>

          {revealedToken && (
            <div className="rounded-lg border border-border bg-surface-2 px-4 py-3 space-y-2">
              <div className="flex items-center justify-between">
                <div className="detail-label">New token</div>
                <button
                  type="button"
                  onClick={copyToken}
                  className="flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground transition-colors mono"
                >
                  {copied ? <Check className="h-3.5 w-3.5 text-healthy" /> : <Copy className="h-3.5 w-3.5" />}
                  {copied ? "Copied" : "Copy"}
                </button>
              </div>
              <div className="text-xs mono text-primary break-all">{revealedToken}</div>
              <div className="text-[11px] text-muted-foreground">
                Save this now — it won't be shown again.
              </div>
            </div>
          )}

          <button
            type="button"
            onClick={() => void rotate()}
            disabled={busy}
            className="btn-primary text-xs disabled:opacity-50"
          >
            {busy ? "Working…" : "Rotate token"}
          </button>
        </div>
      </div>
    </div>
  );
}
