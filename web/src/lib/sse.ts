import type { ChatEvent } from "./types";

type EventHandler = (event: ChatEvent) => void;
type StatusHandler = (status: "streaming" | "idle") => void;

/**
 * SSE-based chat client for the Fanout chat protocol.
 *
 * Sends messages via POST /api/chat and reads streaming SSE responses.
 * Cancel and clear are separate POST endpoints.
 *
 * This is a plain class (not a React hook) so the Zustand store can
 * own the instance and manage its lifetime.
 */
export class ChatClient {
  private baseUrl: string;
  private token?: string;
  private sessionId: string;
  private abortController?: AbortController;
  onEvent: EventHandler;
  onStatus: StatusHandler;

  constructor(
    baseUrl: string,
    onEvent: EventHandler,
    onStatus: StatusHandler,
  ) {
    this.baseUrl = baseUrl;
    this.onEvent = onEvent;
    this.onStatus = onStatus;
    this.sessionId = crypto.randomUUID();
  }

  /** Send a message and stream the response via SSE. */
  async send(msg: {
    content: string;
    window?: number;
    namespace?: string;
  }): Promise<void> {
    this.abortController = new AbortController();
    this.onStatus("streaming");

    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
        "X-Session-ID": this.sessionId,
      };
      if (this.token) {
        headers["Authorization"] = `Bearer ${this.token}`;
      }

      const resp = await fetch(`${this.baseUrl}/api/chat`, {
        method: "POST",
        headers,
        body: JSON.stringify(msg),
        signal: this.abortController.signal,
      });

      if (!resp.ok) {
        this.onEvent({ type: "error", error: `HTTP ${resp.status}` });
        this.onStatus("idle");
        return;
      }

      if (!resp.body) {
        this.onEvent({ type: "error", error: "Response body is empty" });
        this.onStatus("idle");
        return;
      }
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";

        let currentEvent = "";
        for (const line of lines) {
          if (line.startsWith("event: ")) {
            currentEvent = line.slice(7);
          } else if (line.startsWith("data: ") && currentEvent) {
            try {
              const data = JSON.parse(line.slice(6));
              this.onEvent({ type: currentEvent, ...data } as ChatEvent);
            } catch (err) {
              console.warn("[ChatClient] malformed SSE data, skipping:", line.slice(6, 100), err);
            }
            currentEvent = "";
          }
        }
      }
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === "AbortError") {
        // Intentional cancel — not an error
      } else {
        console.error("[ChatClient] stream error:", err);
        this.onEvent({
          type: "error",
          error: "Connection lost. Please try again.",
        });
      }
    } finally {
      this.abortController = undefined;
      this.onStatus("idle");
    }
  }

  /** Cancel the in-flight request. */
  cancel(): void {
    this.abortController?.abort();
    // Also notify server so it cancels the orchestrator
    const headers: Record<string, string> = {
      "X-Session-ID": this.sessionId,
    };
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    }
    fetch(`${this.baseUrl}/api/chat/cancel`, {
      method: "POST",
      headers,
    }).catch((err) => console.warn("[ChatClient] cancel request failed:", err));
  }

  /** Clear conversation history on the server. */
  clear(): void {
    const headers: Record<string, string> = {
      "X-Session-ID": this.sessionId,
    };
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`;
    }
    fetch(`${this.baseUrl}/api/chat/clear`, {
      method: "POST",
      headers,
    }).catch((err) => console.warn("[ChatClient] clear request failed:", err));
  }

  /** Set the auth token for subsequent requests. */
  setToken(token?: string): void {
    this.token = token;
  }
}
