import type { Mock } from "vitest";
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
    let refresh: Mock;

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
        vi.useFakeTimers();
        sources = [];
        refresh = vi.fn().mockName('refresh');
    });

    afterEach(() => {
        vi.useRealTimers();
    });

    it('uses the events stream and a 60-second fallback by default', () => {
        expect(WALL_EVENTS_URL).toBe('/api/v1/wall/events');
        expect(WALL_FALLBACK_REFRESH_MS).toBe(60000);
    });

    it('refreshes when the server reports a change', () => {
        const live = create();
        live.start();
        sources[0].emit('open');
        refresh.mockClear();

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
        const live = create(60000);
        live.start();
        sources[0].emit('open');
        refresh.mockClear();

        vi.advanceTimersByTime(59999);
        expect(refresh).not.toHaveBeenCalled();

        vi.advanceTimersByTime(1);
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

        vi.advanceTimersByTime(2999);
        expect(sources.length).toBe(1);
        vi.advanceTimersByTime(1);
        expect(sources.length).toBe(2);

        sources[1].readyState = FakeSource.CLOSED;
        sources[1].emit('error');
        vi.advanceTimersByTime(5999);
        expect(sources.length).toBe(2);
        vi.advanceTimersByTime(1);
        expect(sources.length).toBe(3);
        live.stop();
    });

    it('resets the backoff once a stream opens again', () => {
        const live = create();
        live.start();
        sources[0].readyState = FakeSource.CLOSED;
        sources[0].emit('error');
        vi.advanceTimersByTime(3000);
        sources[1].emit('open');

        sources[1].readyState = FakeSource.CLOSED;
        sources[1].emit('error');
        vi.advanceTimersByTime(3000);
        expect(sources.length).toBe(3);
        live.stop();
    });

    it('does not reconnect while the browser is still retrying on its own', () => {
        const live = create();
        live.start();
        sources[0].readyState = 0;
        sources[0].emit('error');
        vi.advanceTimersByTime(60000);
        expect(sources.length).toBe(1);
        expect(sources[0].closed).toBe(false);
        live.stop();
    });

    it('stops the stream and every timer when stopped', () => {
        const live = create();
        live.start();
        sources[0].emit('open');
        sources[0].readyState = FakeSource.CLOSED;
        sources[0].emit('error');

        live.stop();
        refresh.mockClear();
        vi.advanceTimersByTime(120000);

        expect(sources.length).toBe(1);
        expect(refresh).not.toHaveBeenCalled();
    });

    it('closes the open stream when stopped', () => {
        const live = create();
        live.start();

        live.stop();

        expect(sources[0].closed).toBe(true);
    });
});
