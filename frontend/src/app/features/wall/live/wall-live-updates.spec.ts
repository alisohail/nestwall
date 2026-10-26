import { WALL_EVENTS_URL, WALL_FALLBACK_REFRESH_MS, WallLiveUpdates } from './wall-live-updates';

class FakeSource {
  static readonly CLOSED = 2;
  readyState = 0;
  closed = false;
  private listeners = new Map<string, (() => void)[]>();

  addEventListener(type: string, listener: () => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  emit(type: string): void {
    (this.listeners.get(type) ?? []).forEach((listener) => listener());
  }

  close(): void {
    this.closed = true;
    this.readyState = FakeSource.CLOSED;
  }
}

describe('WallLiveUpdates', () => {
  let sources: FakeSource[];
  let refresh: jasmine.Spy<() => void>;

  function create(fallbackMs = WALL_FALLBACK_REFRESH_MS): WallLiveUpdates {
    return new WallLiveUpdates(refresh, {
      fallbackMs,
      createSource: () => {
        const source = new FakeSource();
        sources.push(source);
        return source as unknown as EventSource;
      },
    });
  }

  beforeEach(() => {
    jasmine.clock().install();
    sources = [];
    refresh = jasmine.createSpy('refresh');
  });

  afterEach(() => {
    jasmine.clock().uninstall();
  });

  it('uses the events stream and a 60-second fallback by default', () => {
    expect(WALL_EVENTS_URL).toBe('/api/v1/wall/events');
    expect(WALL_FALLBACK_REFRESH_MS).toBe(60_000);
  });

  it('refreshes when the server reports a change', () => {
    const live = create();
    live.start();
    sources[0].emit('open');
    refresh.calls.reset();

    sources[0].emit('changed');

    expect(refresh).toHaveBeenCalledTimes(1);
    live.stop();
  });

  it('refreshes each time the stream opens, to catch up on missed changes', () => {
    const live = create();
    live.start();

    sources[0].emit('open');

    expect(refresh).toHaveBeenCalledTimes(1);
    live.stop();
  });

  it('refreshes on the fallback interval even without a stream message', () => {
    const live = create(60_000);
    live.start();
    sources[0].emit('open');
    refresh.calls.reset();

    jasmine.clock().tick(59_999);
    expect(refresh).not.toHaveBeenCalled();

    jasmine.clock().tick(1);
    expect(refresh).toHaveBeenCalledTimes(1);
    live.stop();
  });

  it('recreates a stream the browser closed, backing off between attempts', () => {
    const live = create();
    live.start();
    sources[0].emit('open');

    sources[0].readyState = FakeSource.CLOSED;
    sources[0].emit('error');
    expect(sources.length).toBe(1);

    jasmine.clock().tick(2_999);
    expect(sources.length).toBe(1);
    jasmine.clock().tick(1);
    expect(sources.length).toBe(2);

    sources[1].readyState = FakeSource.CLOSED;
    sources[1].emit('error');
    jasmine.clock().tick(5_999);
    expect(sources.length).toBe(2);
    jasmine.clock().tick(1);
    expect(sources.length).toBe(3);
    live.stop();
  });

  it('resets the backoff once a stream opens again', () => {
    const live = create();
    live.start();
    sources[0].readyState = FakeSource.CLOSED;
    sources[0].emit('error');
    jasmine.clock().tick(3_000);
    sources[1].emit('open');

    sources[1].readyState = FakeSource.CLOSED;
    sources[1].emit('error');
    jasmine.clock().tick(3_000);
    expect(sources.length).toBe(3);
    live.stop();
  });

  it('does not reconnect while the browser is still retrying on its own', () => {
    const live = create();
    live.start();
    sources[0].readyState = 0;
    sources[0].emit('error');
    jasmine.clock().tick(60_000);
    expect(sources.length).toBe(1);
    expect(sources[0].closed).toBeFalse();
    live.stop();
  });

  it('stops the stream and every timer when stopped', () => {
    const live = create();
    live.start();
    sources[0].emit('open');
    sources[0].readyState = FakeSource.CLOSED;
    sources[0].emit('error');

    live.stop();
    refresh.calls.reset();
    jasmine.clock().tick(120_000);

    expect(sources.length).toBe(1);
    expect(refresh).not.toHaveBeenCalled();
  });

  it('closes the open stream when stopped', () => {
    const live = create();
    live.start();

    live.stop();

    expect(sources[0].closed).toBeTrue();
  });
});
