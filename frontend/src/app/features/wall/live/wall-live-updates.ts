/** The server-sent events stream that tells a wall display when the family's data changed. */
export const WALL_EVENTS_URL = '/api/v1/wall/events';

/** How often a wall display reloads on its own, in case a change signal was missed. */
export const WALL_FALLBACK_REFRESH_MS = 60_000;

const RETRY_MIN_MS = 3_000;
const RETRY_MAX_MS = 60_000;

export interface WallLiveOptions {
  url?: string;
  fallbackMs?: number;
  createSource?: (url: string) => EventSource;
}

/**
 * Keeps a wall display current. onRefresh runs when the server reports a change, each time the stream opens
 * (so a display catches up after a network drop or an API restart), and every fallbackMs as a safety net.
 * The browser reconnects a dropped stream by itself. A stream the browser gives up on is recreated with backoff.
 */
export class WallLiveUpdates {
  private readonly url: string;
  private readonly fallbackMs: number;
  private readonly createSource: (url: string) => EventSource;
  private source: EventSource | null = null;
  private fallbackTimer: ReturnType<typeof setInterval> | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private retryDelay = RETRY_MIN_MS;

  constructor(
    private readonly onRefresh: () => void,
    options: WallLiveOptions = {},
  ) {
    this.url = options.url ?? WALL_EVENTS_URL;
    this.fallbackMs = options.fallbackMs ?? WALL_FALLBACK_REFRESH_MS;
    this.createSource = options.createSource ?? ((url) => new EventSource(url));
  }

  start(): void {
    if (this.fallbackTimer !== null) return;
    this.fallbackTimer = setInterval(() => this.onRefresh(), this.fallbackMs);
    this.connect();
  }

  stop(): void {
    if (this.fallbackTimer !== null) clearInterval(this.fallbackTimer);
    this.fallbackTimer = null;
    if (this.retryTimer !== null) clearTimeout(this.retryTimer);
    this.retryTimer = null;
    this.source?.close();
    this.source = null;
  }

  private connect(): void {
    const source = this.createSource(this.url);
    this.source = source;
    source.addEventListener('open', () => {
      this.retryDelay = RETRY_MIN_MS;
      this.onRefresh();
    });
    source.addEventListener('changed', () => this.onRefresh());
    source.addEventListener('error', () => {
      if (source.readyState === EventSource.CLOSED) this.scheduleReconnect(source);
    });
  }

  private scheduleReconnect(source: EventSource): void {
    source.close();
    if (this.source === source) this.source = null;
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.connect();
    }, this.retryDelay);
    this.retryDelay = Math.min(this.retryDelay * 2, RETRY_MAX_MS);
  }
}
