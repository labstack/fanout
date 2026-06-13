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

      // SSE accumulator state: fields collect until a blank line dispatches the
      // event. Defaults to the "message" event type per the SSE spec, supports
      // multi-line `data:`, and tolerates CRLF line endings.
      let eventType = "";
      let dataLines: string[] = [];

      const dispatch = () => {
        if (dataLines.length === 0) {
          eventType = "";
          return;
        }
        const type = eventType || "message";
        const payload = dataLines.join("\n");
        eventType = "";
        dataLines = [];
        try {
          const data = JSON.parse(payload);
          this.onEvent({ type, ...data } as ChatEvent);
        } catch (err) {
          console.warn("[ChatClient] malformed SSE data, skipping:", payload.slice(0, 100), err);
        }
      };

      const handleLine = (raw: string) => {
        // Strip a trailing CR (CRLF servers) so field names/values stay clean.
        const line = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
        if (line === "") {
          dispatch();
          return;
        }
        if (line.startsWith(":")) return; // comment line
        const colon = line.indexOf(":");
        const field = colon === -1 ? line : line.slice(0, colon);
        // A single leading space after the colon is part of the syntax, not data.
        let value = colon === -1 ? "" : line.slice(colon + 1);
        if (value.startsWith(" ")) value = value.slice(1);
        if (field === "event") eventType = value;
        else if (field === "data") dataLines.push(value);
        // id/retry fields are intentionally ignored
      };

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() ?? "";
        for (const line of lines) handleLine(line);
      }

      // Flush any trailing line + pending event the server left unterminated.
      if (buffer) handleLine(buffer);
      dispatch();
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
