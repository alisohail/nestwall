import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { Observable, of, throwError } from 'rxjs';
import { ApiClient, ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { DiagnosticEventsResponse, DiagnosticsComponent, DiagnosticsResponse } from './diagnostics.component';

describe('DiagnosticsComponent', () => {
  let fixture: ComponentFixture<DiagnosticsComponent>;
  let api: jasmine.SpyObj<ApiClient>;
  let grants: string[];
  let eventsSample: DiagnosticEventsResponse;

  const sample: DiagnosticsResponse = {
    version: '1.2.3',
    uptime_seconds: 90061,
    database_bytes: 2048,
    migrations: { applied: 12, latest: '0012_add_sync' },
    last_backup: { kind: 'scheduled', status: 'ok', finished_at: '2025-01-02T03:04:05Z' },
    last_successful_backup_at: '2025-01-02T03:04:05Z',
    sync: { linked_calendars: 2, paused: 1, failing: 0, last_synced_at: null },
    email: { pending: 3, failed: 1, sent_last_24h: 7 },
    wall_devices: { active: 2, last_seen_at: '2025-01-02T04:00:00Z' },
    server_errors: { since_start_count: 4, last_at: null },
  };

  function build(): void {
    fixture = TestBed.createComponent(DiagnosticsComponent);
    fixture.detectChanges();
  }

  it('lists recent events with a translated label and the request path for server errors', () => {
    eventsSample = {
      max: 100,
      events: [
        { id: 'e1', kind: 'server_error', code: 'http_500', status: 500, path: '/api/v1/x', created_at: '2025-01-02T03:04:05Z' },
        { id: 'e2', kind: 'calendar_sync', code: 'unavailable', status: null, path: null, created_at: '2025-01-02T02:00:00Z' },
      ],
    };
    build();

    const items = fixture.nativeElement.querySelectorAll('[data-testid="diagnostics-events-list"] li');
    expect(items.length).toBe(2);
    expect(items[0].textContent).toContain('HTTP 500');
    expect(items[0].textContent).toContain('/api/v1/x');
    expect(items[1].textContent).toContain('Calendar could not be fetched');
    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-events-empty"]')).toBeNull();
  });

  it('shows an empty note when there are no recent events, and disables Clear', () => {
    build();

    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-events-empty"]')).not.toBeNull();
    const clear = fixture.nativeElement.querySelector('[data-testid="diagnostics-events-clear"]') as HTMLButtonElement;
    expect(clear.disabled).toBeTrue();
  });

  it('clears events only after a second confirm, then reloads the page', () => {
    eventsSample = {
      max: 100,
      events: [{ id: 'e1', kind: 'email', code: 'send_failed', status: null, path: null, created_at: '2025-01-02T03:04:05Z' }],
    };
    build();

    (fixture.nativeElement.querySelector('[data-testid="diagnostics-events-clear"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(api.delete).not.toHaveBeenCalled();

    (fixture.nativeElement.querySelector('[data-testid="diagnostics-events-confirm"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(api.delete).toHaveBeenCalledWith('/api/v1/settings/diagnostics/events');
    expect(api.get).toHaveBeenCalledWith('/api/v1/settings/diagnostics/events');
  });

  // Routes the diagnostics call to `main` and the events call to `eventsSample`.
  function respondWith(main: Observable<unknown>): void {
    api.get.and.callFake(<T>(url: string) => (url.endsWith('/events') ? of(eventsSample) : main) as Observable<T>);
  }

  beforeEach(() => {
    grants = ['settings.view'];
    eventsSample = { max: 100, events: [] };
    api = jasmine.createSpyObj<ApiClient>('ApiClient', ['get', 'delete']);
    respondWith(of(sample));
    api.delete.and.returnValue(of({ deleted: 2 }));

    TestBed.configureTestingModule({
      imports: [DiagnosticsComponent, getTranslocoTestingModule()],
      providers: [
        { provide: ApiClient, useValue: api },
        provideRouter([]),
        {
          provide: AuthService,
          useValue: {
            can: (perm: string) => grants.includes(perm),
            loadPermissions: (): Observable<void> => of(undefined),
          },
        },
      ],
    });
  });

  it('shows a permission banner and does not call the API without settings.view', () => {
    grants = [];
    build();

    expect(fixture.nativeElement.querySelector('app-banner')).not.toBeNull();
    expect(api.get).not.toHaveBeenCalled();
    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-app"]')).toBeNull();
  });

  it('loads the diagnostics and shows version, migrations, backups and sync', () => {
    build();

    expect(api.get).toHaveBeenCalledWith('/api/v1/settings/diagnostics');
    const text = (id: string): string => fixture.nativeElement.querySelector(`[data-testid="${id}"]`).textContent.trim();
    expect(text('diagnostics-version')).toBe('1.2.3');
    expect(text('diagnostics-migrations')).toContain('12');
    expect(text('diagnostics-migrations')).toContain('0012_add_sync');
    expect(text('diagnostics-last-backup')).toContain('scheduled');
    expect(text('diagnostics-linked')).toBe('2');
    expect(text('diagnostics-paused')).toBe('1');
    expect(text('diagnostics-failing')).toBe('0');
  });

  it('shows Never for a sync time that has not happened', () => {
    build();

    const el = fixture.nativeElement.querySelector('[data-testid="diagnostics-last-synced"]');
    expect(el.textContent).toContain('Never');
  });

  it('shows a stale backup warning when the last good backup is over seven days old', () => {
    build();

    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-backup-stale"]')).not.toBeNull();
  });

  it('hides the stale backup warning when a recent backup succeeded', () => {
    respondWith(of({ ...sample, last_successful_backup_at: new Date().toISOString() }));
    build();

    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-backup-stale"]')).toBeNull();
  });

  it('shows a status badge per card: problem for failed email, check for paused sync, OK when healthy', () => {
    build();

    const badge = (id: string): HTMLElement => fixture.nativeElement.querySelector(`[data-testid="${id}"]`);
    expect(badge('diagnostics-email-status').classList).toContain('err');
    expect(badge('diagnostics-sync-status').classList).toContain('amber');
    expect(badge('diagnostics-backup-status').classList).toContain('amber');
    expect(badge('diagnostics-errors-status').classList).toContain('amber');
  });

  it('shows OK badges when nothing needs attention', () => {
    respondWith(
      of({
        ...sample,
        last_successful_backup_at: new Date().toISOString(),
        sync: { ...sample.sync, paused: 0 },
        email: { pending: 0, failed: 0, sent_last_24h: 7 },
        server_errors: { since_start_count: 0, last_at: null },
      }),
    );
    build();

    const badge = (id: string): HTMLElement => fixture.nativeElement.querySelector(`[data-testid="${id}"]`);
    expect(badge('diagnostics-email-status').classList).toContain('ok');
    expect(badge('diagnostics-sync-status').classList).toContain('ok');
    expect(badge('diagnostics-backup-status').classList).toContain('ok');
    expect(badge('diagnostics-errors-status').classList).toContain('ok');
  });

  it('shows email, wall device and server error counts, plus uptime and database size', () => {
    build();

    const text = (id: string): string => fixture.nativeElement.querySelector(`[data-testid="${id}"]`).textContent.trim();
    expect(text('diagnostics-uptime')).toBe('1 d 1 h 1 min');
    expect(text('diagnostics-database')).toBe('2.0 KB');
    expect(text('diagnostics-email-pending')).toBe('3');
    expect(text('diagnostics-email-failed')).toBe('1');
    expect(text('diagnostics-email-sent')).toBe('7');
    expect(text('diagnostics-wall-active')).toBe('2');
    expect(text('diagnostics-wall-last-seen')).toContain('2025');
    expect(text('diagnostics-errors-count')).toBe('4');
    expect(text('diagnostics-errors-last')).toBe('None');
  });

  it('shows Unknown when the database size is not available', () => {
    respondWith(of({ ...sample, database_bytes: null }));
    build();

    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-database"]').textContent).toContain('Unknown');
  });

  it('shows the full-page load error and retries when loading fails', () => {
    respondWith(throwError(() => ({ code: 'internal', status: 500 }) as ApiError));
    build();

    expect(fixture.nativeElement.querySelector('app-loading')).toBeNull();
    expect(fixture.nativeElement.querySelector('app-load-error')).not.toBeNull();

    respondWith(of(sample));
    fixture.nativeElement.querySelector('[data-testid="load-error-retry"]').click();
    fixture.detectChanges();

    const mainCalls = api.get.calls.allArgs().filter((args) => args[0] === '/api/v1/settings/diagnostics');
    expect(mainCalls.length).toBe(2);
    expect(fixture.nativeElement.querySelector('app-load-error')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-app"]')).not.toBeNull();
  });

  it('shows an error and retries when loading fails', () => {
    respondWith(throwError(() => new Error('down')));
    build();

    expect(fixture.componentInstance.loadState()).toBe('error');
    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-app"]')).toBeNull();

    respondWith(of(sample));
    fixture.componentInstance.load();
    fixture.detectChanges();

    expect(fixture.componentInstance.loadState()).toBe('ready');
    expect(fixture.nativeElement.querySelector('[data-testid="diagnostics-app"]')).not.toBeNull();
  });
});
