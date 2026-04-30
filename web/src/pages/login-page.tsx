import { useEffect, useState } from "react";
import { Navigate, useSearchParams } from "react-router";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Check, Copy, Loader2, Radio } from "lucide-react";
import { toast } from "sonner";
import { setApiToken } from "@/api/client";
import { useAuth } from "@/hooks/auth-context";
import { AuthShell } from "@/components/layout/auth-shell";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { ErrorState } from "@/components/states/error-state";

type Step = "loading" | "setup" | "token_shown" | "email" | "code";

const setupSchema = z.object({
  email: z.string().email("Enter a valid email"),
  name: z.string().optional(),
  setupToken: z.string().min(1, "Setup token is required"),
});

const emailSchema = z.object({
  email: z.string().email("Enter a valid email"),
});

const codeSchema = z.object({
  code: z.string().regex(/^\d{6}$/, "Enter the 6-digit code"),
});

type SetupValues = z.infer<typeof setupSchema>;
type EmailValues = z.infer<typeof emailSchema>;
type CodeValues = z.infer<typeof codeSchema>;

export function LoginPage() {
  const { user, isLoading, login } = useAuth();
  const [searchParams] = useSearchParams();
  const next = searchParams.get("next") || "/";

  const [step, setStep] = useState<Step>("loading");
  const [verifyEmail, setVerifyEmail] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [accessToken, setAccessToken] = useState<string | null>(null);
  const [ingestToken, setIngestToken] = useState<string | null>(null);
  const [ingestHeaderName, setIngestHeaderName] = useState(
    "x-fanout-ingest-token",
  );
  const [copied, setCopied] = useState(false);

  const setupForm = useForm<SetupValues>({
    resolver: zodResolver(setupSchema),
    defaultValues: { email: "", name: "", setupToken: "" },
  });
  const emailForm = useForm<EmailValues>({
    resolver: zodResolver(emailSchema),
    defaultValues: { email: "" },
  });
  const codeForm = useForm<CodeValues>({
    resolver: zodResolver(codeSchema),
    defaultValues: { code: "" },
  });

  useEffect(() => {
    (async () => {
      try {
        const r = await fetch("/api/auth/status");
        if (!r.ok) throw new Error(`auth status HTTP ${r.status}`);
        const status = await r.json();
        setStep(status.setup_required ? "setup" : "email");
      } catch (err) {
        console.error("[LoginPage] auth status probe failed:", err);
        setError("Could not contact server. Check connectivity and reload.");
        setStep("email");
      }
    })();
  }, []);

  if (!isLoading && user) {
    return <Navigate to={next} replace />;
  }

  async function handleSetup(values: SetupValues) {
    setError(null);
    try {
      const result = await fetch("/api/auth/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          email: values.email.trim().toLowerCase(),
          name: values.name?.trim() ?? "",
          setup_token: values.setupToken.trim(),
        }),
      });
      if (!result.ok) {
        const body = await result.json().catch(() => ({}));
        setError(
          body.detail || body.message || `Setup failed (HTTP ${result.status})`,
        );
        return;
      }
      const data = await result.json();
      setApiToken(data.access_token);
      setAccessToken(data.access_token);
      setIngestToken(data.ingest_token || null);
      if (data.ingest_header_name) setIngestHeaderName(data.ingest_header_name);
      setStep("token_shown");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Setup failed");
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
    } catch (err) {
      console.warn("[LoginPage] clipboard write failed:", err);
      toast.error("Couldn't copy. Select the token manually — it won't be shown again.");
    }
  }

  async function handleEmail(values: EmailValues) {
    setError(null);
    try {
      const res = await fetch("/api/auth/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: values.email.trim().toLowerCase() }),
      });
      const body = await res.json().catch(() => ({}));
      if (res.ok && body.code_sent) {
        setVerifyEmail(values.email.trim().toLowerCase());
        setStep("code");
      } else {
        setError(
          body.detail || body.message || "Unable to send verification code.",
        );
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to send code");
    }
  }

  async function handleCode(values: CodeValues) {
    setError(null);
    try {
      const result = await fetch("/api/auth/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ email: verifyEmail, code: values.code }),
      });
      if (!result.ok) {
        const body = await result.json().catch(() => ({}));
        setError(
          body.detail ||
            body.message ||
            `Invalid or expired code (HTTP ${result.status})`,
        );
        return;
      }
      const data = await result.json();
      setApiToken(data.access_token);
      await login(data.access_token);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Verification failed");
    }
  }

  if (step === "loading") {
    return (
      <AuthShell>
        <div className="flex justify-center">
          <Loader2
            className="size-6 animate-spin text-primary"
            aria-label="Loading"
          />
        </div>
      </AuthShell>
    );
  }

  return (
    <AuthShell className="max-w-md">
      <div className="mb-6 space-y-3 text-center">
        <div className="inline-flex items-center justify-center">
          <Radio className="size-8 text-primary" aria-hidden="true" />
        </div>
        <h1 className="font-heading text-2xl font-bold text-foreground">
          Fanout
        </h1>
        <p className="text-sm text-muted-foreground">
          {step === "setup" &&
            "Create your admin account with the setup token shown in the server output"}
          {step === "token_shown" &&
            "Save your ingest token — it won't be shown again"}
          {step === "email" && "Sign in to your account"}
          {step === "code" && `Enter the code sent to ${verifyEmail}`}
        </p>
      </div>

      {error && <ErrorState error={error} className="mb-4" />}

      {step === "setup" && (
        <Card>
          <CardContent className="pt-6">
            <Form {...setupForm}>
              <form
                onSubmit={setupForm.handleSubmit(handleSetup)}
                className="space-y-4"
              >
                <FormField
                  control={setupForm.control}
                  name="email"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Email</FormLabel>
                      <FormControl>
                        <Input
                          type="email"
                          placeholder="admin@company.com"
                          autoComplete="email"
                          autoFocus
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={setupForm.control}
                  name="name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Name (optional)</FormLabel>
                      <FormControl>
                        <Input placeholder="Your name" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={setupForm.control}
                  name="setupToken"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Setup token</FormLabel>
                      <FormControl>
                        <Input
                          type="password"
                          placeholder="Paste the setup token from the server output"
                          autoComplete="one-time-code"
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        Fanout prints this token in the server output while no
                        admin exists.
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <Button
                  type="submit"
                  className="w-full"
                  disabled={setupForm.formState.isSubmitting}
                >
                  {setupForm.formState.isSubmitting ? (
                    <>
                      <Loader2 className="size-4 animate-spin" /> Setting up…
                    </>
                  ) : (
                    "Create admin account"
                  )}
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>
      )}

      {step === "token_shown" && ingestToken && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">Collector configuration</CardTitle>
            <CardDescription>
              This token won't be shown again. Rotate from settings if you lose
              it.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2 rounded-lg border border-border bg-surface-2 px-4 py-3">
              <div className="flex items-center justify-between">
                <div className="detail-label">Configuration</div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => void copyIngestConfig()}
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
              <pre className="whitespace-pre-wrap break-all font-mono text-xs leading-6 text-foreground/80">
                {`OTEL_EXPORTER_OTLP_ENDPOINT=<host>:4317
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_EXPORTER_OTLP_HEADERS=${ingestHeaderName}=`}
                <span className="text-primary">{ingestToken}</span>
              </pre>
            </div>
            <Button
              type="button"
              className="w-full"
              onClick={() => void continueAfterTokenShown()}
            >
              Continue to Fanout
            </Button>
          </CardContent>
        </Card>
      )}

      {step === "email" && (
        <Card>
          <CardContent className="pt-6">
            <Form {...emailForm}>
              <form
                onSubmit={emailForm.handleSubmit(handleEmail)}
                className="space-y-4"
              >
                <FormField
                  control={emailForm.control}
                  name="email"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Email</FormLabel>
                      <FormControl>
                        <Input
                          type="email"
                          placeholder="you@company.com"
                          autoComplete="email"
                          autoFocus
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <Button
                  type="submit"
                  className="w-full"
                  disabled={emailForm.formState.isSubmitting}
                >
                  {emailForm.formState.isSubmitting ? (
                    <>
                      <Loader2 className="size-4 animate-spin" /> Sending code…
                    </>
                  ) : (
                    "Continue"
                  )}
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>
      )}

      {step === "code" && (
        <Card>
          <CardContent className="pt-6">
            <Form {...codeForm}>
              <form
                onSubmit={codeForm.handleSubmit(handleCode)}
                className="space-y-4"
              >
                <FormField
                  control={codeForm.control}
                  name="code"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Verification code</FormLabel>
                      <FormControl>
                        <Input
                          inputMode="numeric"
                          autoComplete="one-time-code"
                          maxLength={6}
                          autoFocus
                          placeholder="000000"
                          className="text-center font-mono text-2xl tracking-[0.5em]"
                          value={field.value}
                          onChange={(e) =>
                            field.onChange(
                              e.target.value.replace(/\D/g, "").slice(0, 6),
                            )
                          }
                          onBlur={field.onBlur}
                          name={field.name}
                          ref={field.ref}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <Button
                  type="submit"
                  className="w-full"
                  disabled={codeForm.formState.isSubmitting}
                >
                  {codeForm.formState.isSubmitting ? (
                    <>
                      <Loader2 className="size-4 animate-spin" /> Verifying…
                    </>
                  ) : (
                    "Verify"
                  )}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="w-full font-mono text-xs"
                  onClick={() => {
                    setStep("email");
                    codeForm.reset({ code: "" });
                    setError(null);
                  }}
                >
                  Use a different email
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>
      )}
    </AuthShell>
  );
}
