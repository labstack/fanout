import { useState } from "react";
import { Navigate } from "react-router";
import { useMutation } from "@tanstack/react-query";
import { Check, Copy, KeyRound, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { api, isApiError } from "@/api/client";
import { useAuth } from "@/hooks/auth-context";
import { PageContainer } from "@/components/layout/page-container";
import { PageHeader } from "@/components/ui/page-header";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { StatusBadge } from "@/components/ui/status-badge";

interface RotateResponse {
  ingest_token: string;
}

interface ApiKeyResponse {
  api_key: string;
}

type ConfirmKind = "rotate" | "regen" | "revoke";

const CONFIRM_COPY: Record<
  ConfirmKind,
  { title: string; description: string; confirmLabel: string }
> = {
  rotate: {
    title: "Rotate ingest token?",
    description: "The previous token stops working immediately — collectors using it will fail until updated.",
    confirmLabel: "Rotate token",
  },
  regen: {
    title: "Regenerate API key?",
    description: "The current key stops working immediately. Any client using it loses access.",
    confirmLabel: "Regenerate key",
  },
  revoke: {
    title: "Revoke API key?",
    description: "Any client using it loses access immediately. This cannot be undone.",
    confirmLabel: "Revoke key",
  },
};

export function SettingsPage() {
  const { isAdmin, isLoading: authLoading, user } = useAuth();
  const [revealedToken, setRevealedToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const [revealedKey, setRevealedKey] = useState<string | null>(null);
  const [keyCopied, setKeyCopied] = useState(false);
  // Tracked locally: the context user is fetched once at login, so mirror the
  // existence flag here and update it as the admin generates/revokes the key.
  const [hasKey, setHasKey] = useState(user?.has_api_key ?? false);

  const [confirm, setConfirm] = useState<ConfirmKind | null>(null);

  const rotateMutation = useMutation({
    mutationFn: () =>
      api<RotateResponse>("/api/settings/ingest/rotate-token", { method: "POST" }),
    onSuccess: (r) => {
      if (!r.ingest_token) {
        toast.error("Server returned an empty token");
        return;
      }
      setRevealedToken(r.ingest_token);
    },
    onError: (err) =>
      toast.error(isApiError(err) ? err.message : "Failed to rotate token"),
    onSettled: () => setConfirm(null),
  });

  const generateKeyMutation = useMutation({
    mutationFn: () => api<ApiKeyResponse>("/api/auth/api-key", { method: "POST" }),
    onSuccess: (r) => {
      if (!r.api_key) {
        toast.error("Server returned an empty key");
        return;
      }
      setRevealedKey(r.api_key);
      setHasKey(true);
    },
    onError: (err) => {
      // Generation rotates the key server-side before the response returns, so
      // a lost response may mean the previous key is already dead. Never leave
      // a stale key on screen labelled "save this" — clear it and tell the user
      // the old one may no longer work.
      setRevealedKey(null);
      setHasKey(false);
      toast.error(
        isApiError(err)
          ? err.message
          : "Failed to generate API key. The previous key may no longer work — generate again to get a usable one.",
      );
    },
    onSettled: () => setConfirm(null),
  });

  const revokeMutation = useMutation({
    mutationFn: () => api("/api/auth/api-key", { method: "DELETE" }),
    onSuccess: () => {
      setRevealedKey(null);
      setHasKey(false);
    },
    onError: (err) =>
      toast.error(isApiError(err) ? err.message : "Failed to revoke API key"),
    onSettled: () => setConfirm(null),
  });

  if (authLoading) return null;
  if (!isAdmin) return <Navigate to="/" replace />;

  const tokenBusy = rotateMutation.isPending;
  const keyBusy = generateKeyMutation.isPending || revokeMutation.isPending;
  const confirmBusy =
    rotateMutation.isPending ||
    generateKeyMutation.isPending ||
    revokeMutation.isPending;
  // Kept mounted (open toggles) so the close animation plays; copy is null when
  // closed, in which case Radix doesn't render the content anyway.
  const confirmCopy = confirm ? CONFIRM_COPY[confirm] : null;

  function onGenerateClick() {
    // Only confirm when replacing an existing key; first-time generation is safe.
    if (hasKey) setConfirm("regen");
    else generateKeyMutation.mutate();
  }

  function handleConfirm() {
    if (confirm === "rotate") rotateMutation.mutate();
    else if (confirm === "regen") generateKeyMutation.mutate();
    else if (confirm === "revoke") revokeMutation.mutate();
  }

  async function copyToken() {
    if (!revealedToken) return;
    try {
      await navigator.clipboard.writeText(revealedToken);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch (err) {
      console.warn("[Settings] clipboard write failed:", err);
      toast.error("Couldn't copy. Select the token manually and copy with your keyboard.");
    }
  }

  async function copyKey() {
    if (!revealedKey) return;
    try {
      await navigator.clipboard.writeText(revealedKey);
      setKeyCopied(true);
      setTimeout(() => setKeyCopied(false), 1500);
    } catch (err) {
      console.warn("[Settings] clipboard write failed:", err);
      toast.error("Couldn't copy. Select the key manually and copy with your keyboard.");
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
              onClick={() => setConfirm("rotate")}
              disabled={tokenBusy}
            >
              {tokenBusy ? "Working…" : "Rotate token"}
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div className="flex items-start justify-between gap-4">
              <div className="space-y-1">
                <CardTitle className="text-base">API key</CardTitle>
                <CardDescription>
                  A key for programmatic access to the API and MCP endpoint.
                  Send it as <code className="font-mono">Authorization: Bearer fo_…</code>.
                  Each user has at most one key; generating a new one replaces the old.
                </CardDescription>
              </div>
              <StatusBadge variant={hasKey ? "success" : "neutral"} dot={false}>
                <KeyRound className="size-3" aria-hidden="true" />
                {hasKey ? "Active" : "None"}
              </StatusBadge>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            {revealedKey ? (
              <div className="space-y-2 rounded-lg border border-border bg-surface-2 px-4 py-3">
                <div className="flex items-center justify-between">
                  <div className="detail-label">New key</div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => void copyKey()}
                    className="h-auto gap-1 px-2 py-1 font-mono text-[11px]"
                  >
                    {keyCopied ? (
                      <Check className="size-3.5 text-success" />
                    ) : (
                      <Copy className="size-3.5" />
                    )}
                    {keyCopied ? "Copied" : "Copy"}
                  </Button>
                </div>
                <div className="break-all font-mono text-xs text-primary">
                  {revealedKey}
                </div>
                <div className="text-[11px] text-muted-foreground">
                  Save this now — it won't be shown again.
                </div>
              </div>
            ) : null}

            <div className="flex gap-2">
              <Button
                type="button"
                size="sm"
                onClick={onGenerateClick}
                disabled={keyBusy}
              >
                {keyBusy ? "Working…" : hasKey ? "Regenerate key" : "Generate key"}
              </Button>
              {hasKey ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => setConfirm("revoke")}
                  disabled={keyBusy}
                >
                  Revoke
                </Button>
              ) : null}
            </div>
          </CardContent>
        </Card>
      </div>

      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => {
          if (!open && !confirmBusy) setConfirm(null);
        }}
        title={confirmCopy?.title ?? ""}
        description={confirmCopy?.description}
        confirmLabel={confirmCopy?.confirmLabel}
        destructive
        loading={confirmBusy}
        onConfirm={handleConfirm}
      />
    </PageContainer>
  );
}
