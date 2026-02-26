import type { ChatEvent, ClientMessage } from "./types";

type EventHandler = (event: ChatEvent) => void;
type StatusHandler = (connected: boolean) => void;

/**
 * Construct a WebSocket URL from the current page location.
 * Automatically selects wss: for HTTPS and ws: for HTTP.
 */
export function wsURL(path: string, token?: string): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${proto}//${location.host}${path}`;
  return token ? `${url}?token=${encodeURIComponent(token)}` : url;
}

/**
 * WebSocket client for the Fanout chat protocol.
 *
 * Handles connection lifecycle, automatic reconnection with exponential
 * backoff + jitter, JSON message parsing, and status callbacks.
 *
 * This is a plain class (not a React hook) so the Zustand store can
 * own the instance and manage its lifetime.
 */
export class ChatSocket {
  private ws: WebSocket | null = null;
  private url: string;
  private onEvent: EventHandler;
  private onStatus: StatusHandler;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectDelay = 1000;
  private maxDelay = 30000;
  private shouldReconnect = true;

  constructor(url: string, onEvent: EventHandler, onStatus: StatusHandler) {
    this.url = url;
    this.onEvent = onEvent;
    this.onStatus = onStatus;
  }

  /** Open the WebSocket connection. Safe to call multiple times. */
  connect(): void {
    // Avoid duplicate connections
    if (this.ws) {
      return;
    }

    this.shouldReconnect = true;

    const ws = new WebSocket(this.url);

    ws.onopen = () => this.handleOpen();
    ws.onmessage = (ev: MessageEvent) => this.handleMessage(ev.data as string);
    ws.onclose = () => this.handleClose();
    ws.onerror = () => {
      // The close event always fires after error, so reconnection is
      // handled there. Nothing extra needed here.
    };

    this.ws = ws;
  }

  /** Send a client message (message, cancel, or clear). Returns false if dropped. */
  send(msg: ClientMessage): boolean {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
      return true;
    }
    console.warn("[ChatSocket] message dropped (not connected):", msg.type);
    return false;
  }

  /**
   * Cleanly close the connection. Reconnection is disabled so this is
   * a permanent shutdown until `connect()` is called again.
   */
  close(): void {
    this.shouldReconnect = false;
    this.clearReconnectTimer();

    if (this.ws) {
      this.ws.close(1000, "client close");
      this.ws = null;
    }
  }

  // --------------- private ---------------

  private handleOpen(): void {
    // Reset backoff on successful connection
    this.reconnectDelay = 1000;
    this.onStatus(true);
  }

  private handleMessage(data: string): void {
    let event: ChatEvent;
    try {
      event = JSON.parse(data);
    } catch (err) {
      console.warn("[ChatSocket] failed to parse message:", err);
      return;
    }
    try {
      this.onEvent(event);
    } catch (err) {
      console.error("[ChatSocket] event handler error:", err);
    }
  }

  private handleClose(): void {
    this.ws = null;
    this.onStatus(false);

    if (this.shouldReconnect) {
      this.scheduleReconnect();
    }
  }

  private scheduleReconnect(): void {
    this.clearReconnectTimer();

    // Add jitter: random 0-25% of the current delay
    const jitter = Math.random() * this.reconnectDelay * 0.25;
    const delay = this.reconnectDelay + jitter;

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.ws = null; // ensure clean state before reconnecting
      this.connect();
    }, delay);

    // Exponential backoff: double the base delay for next attempt, capped
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxDelay);
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }
}
