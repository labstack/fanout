import { useState } from "react";
import { Navigate } from "react-router";
import { Check, Copy, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { api, isApiError } from "@/api/client";
import { useAuth } from "@/hooks/auth-context";
import { PageContainer } from "@/components/layout/page-container";
import { PageHeader } from "@/components/ui/page-header";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status-badge";

interface RotateResponse {
  ingest_token: string;
}

export function SettingsPage() {
  const { isAdmin, isLoading: authLoading } = useAuth();
  const [busy, setBusy] = useState(false);
  const [revealedToken, setRevealedToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  if (authLoading) return null;
  if (!isAdmin) return <Navigate to="/" replace />;

  async function rotate() {
    if (!confirm("Generate a new ingest token? The previous token will stop working.")) return;
    setBusy(true);
    try {
      const r = await api<RotateResponse>("/api/settings/ingest/rotate-token", { method: "POST" });
      if (!r.ingest_token) {
        throw new Error("server returned an empty token");
      }
      setRevealedToken(r.ingest_token);
    } catch (err) {
      toast.error(isApiError(err) ? err.message : "Failed to rotate token");
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
      /* clipboard may be unavailable */
    }
  }

  return (
    <PageContainer>
      <PageHeader title="Settings" subtitle="Admin-only configuration." />

      <div className="mx-auto max-w-3xl space-y-6">
        <Card>
          <CardHeader>
            <div className="flex items-start justify-between gap-4">
              <div className="space-y-1">
                <CardTitle className="text-base">Ingest token</CardTitle>
                <CardDescription>
                  Rotate the token collectors present on every OTLP request.
                  The previous token stops working immediately.
                </CardDescription>
              </div>
              <StatusBadge variant="success" dot={false}>
                <ShieldCheck className="size-3" aria-hidden="true" />
                Required
              </StatusBadge>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            {revealedToken ? (
              <div className="space-y-2 rounded-lg border border-border bg-surface-2 px-4 py-3">
                <div className="flex items-center justify-between">
                  <div className="detail-label">New token</div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => void copyToken()}
                    className="h-auto gap-1 px-2 py-1 font-mono text-[11px]"
                  >
                    {copied ? (
                      <Check className="size-3.5 text-success" />
                    ) : (
                      <Copy className="size-3.5" />
                    )}
                    {copied ? "Copied" : "Copy"}
                  </Button>
                </div>
                <div className="break-all font-mono text-xs text-primary">
                  {revealedToken}
                </div>
                <div className="text-[11px] text-muted-foreground">
                  Save this now — it won't be shown again.
                </div>
              </div>
            ) : null}

            <Button
              type="button"
              size="sm"
              onClick={() => void rotate()}
              disabled={busy}
            >
              {busy ? "Working…" : "Rotate token"}
            </Button>
          </CardContent>
        </Card>
      </div>
    </PageContainer>
  );
}
