import { DatePipe } from '@angular/common';
import { Component, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { ApiClient, ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { TimeFormatService } from '../../../core/time-format/time-format.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';

/** Counts and times only. The API never returns calendar links, names or error text. */
export interface DiagnosticsResponse {
  version: string;
  uptime_seconds: number;
  database_bytes: number | null;
  migrations: { applied: number; latest: string };
  last_backup: { kind: string; status: string; finished_at: string | null } | null;
  last_successful_backup_at: string | null;
  sync: { linked_calendars: number; paused: number; failing: number; last_synced_at: string | null };
  email: { pending: number; failed: number; sent_last_24h: number };
  wall_devices: { active: number; last_seen_at: string | null };
  server_errors: { since_start_count: number; last_at: string | null };
}

/** One recent failure. `code` is a fixed label; `status` and `path` are set for server errors only. */
export interface DiagnosticEvent {
  id: string;
  kind: 'server_error' | 'calendar_sync' | 'email';
  code: string;
  status: number | null;
  path: string | null;
  created_at: string;
}

export interface DiagnosticEventsResponse {
  events: DiagnosticEvent[];
  max: number;
}

/** Backups run once a day (see the backup schedule). Older than a day plus an hour of grace means today's run was missed. */
const BACKUP_STALE_MS = 25 * 60 * 60 * 1000;

/** ok = fine, warn = worth a look, bad = needs attention now. */
export type Tone = 'ok' | 'warn' | 'bad';

const BADGE_CLASS: Record<Tone, string> = { ok: 'ok', warn: 'amber', bad: 'err' };

/** Parent diagnostics page: version, migrations, backups and calendar sync health (`settings.view`). */
@Component({
    selector: 'app-diagnostics',
    imports: [TranslocoModule, DatePipe, BannerComponent, IconComponent, LoadErrorComponent, LoadingComponent],
    templateUrl: './diagnostics.component.html',
    styleUrl: './diagnostics.component.scss'
})
export class DiagnosticsComponent {
  private api = inject(ApiClient);
  private auth = inject(AuthService);
  private destroyRef = inject(DestroyRef);
  timeFormat = inject(TimeFormatService);

  permsReady = signal(false);
  canView = signal(false);
  loadState = signal<'idle' | 'loading' | 'ready' | 'error'>('idle');
  loadError = signal<ApiError | null>(null);
  data = signal<DiagnosticsResponse | null>(null);
  events = signal<DiagnosticEvent[]>([]);
  eventsMax = signal(0);
  eventsError = signal<ApiError | null>(null);
  confirmingClear = signal(false);
  clearing = signal(false);

  constructor() {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.canView.set(this.auth.can('settings.view'));
        this.permsReady.set(true);
        if (this.canView()) {
          this.load();
          this.loadEvents();
        }
      });
  }

  /** True when there is no successful backup, or the last one is more than seven days old. */
  backupStale(d: DiagnosticsResponse): boolean {
    if (!d.last_successful_backup_at) return true;
    return Date.now() - new Date(d.last_successful_backup_at).getTime() > BACKUP_STALE_MS;
  }

  backupTone(d: DiagnosticsResponse): Tone {
    if (!d.last_successful_backup_at) return 'bad';
    return this.backupStale(d) ? 'warn' : 'ok';
  }

  syncTone(d: DiagnosticsResponse): Tone {
    if (d.sync.failing > 0) return 'bad';
    return d.sync.paused > 0 ? 'warn' : 'ok';
  }

  emailTone(d: DiagnosticsResponse): Tone {
    return d.email.failed > 0 ? 'bad' : 'ok';
  }

  errorsTone(d: DiagnosticsResponse): Tone {
    return d.server_errors.since_start_count > 0 ? 'warn' : 'ok';
  }

  badgeClass(tone: Tone): string {
    return BADGE_CLASS[tone];
  }

  /** Uptime as days, hours and minutes, for the translated uptime line. */
  uptime(seconds: number): { days: number; hours: number; minutes: number } {
    const minutes = Math.floor(seconds / 60);
    return { days: Math.floor(minutes / 1440), hours: Math.floor((minutes % 1440) / 60), minutes: minutes % 60 };
  }

  /** Human-readable size. Units are kept as plain SI-style labels. */
  size(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    const units = ['KB', 'MB', 'GB'];
    let value = bytes / 1024;
    let i = 0;
    while (value >= 1024 && i < units.length - 1) {
      value /= 1024;
      i++;
    }
    return `${value.toFixed(1)} ${units[i]}`;
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.api
      .get<DiagnosticsResponse>('/api/v1/settings/diagnostics')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (d) => {
          this.data.set(d);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  loadEvents(): void {
    this.eventsError.set(null);
    this.api
      .get<DiagnosticEventsResponse>('/api/v1/settings/diagnostics/events')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.events.set(res.events);
          this.eventsMax.set(res.max);
        },
        error: (err: ApiError) => this.eventsError.set(err),
      });
  }

  requestClear(): void {
    this.confirmingClear.set(true);
  }

  cancelClear(): void {
    this.confirmingClear.set(false);
  }

  /** Deletes the recent events and resets the server error counter, then refreshes both cards. */
  clearEvents(): void {
    this.clearing.set(true);
    this.api
      .delete<{ deleted: number }>('/api/v1/settings/diagnostics/events')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.clearing.set(false);
          this.confirmingClear.set(false);
          this.loadEvents();
          this.load();
        },
        error: (err: ApiError) => {
          this.clearing.set(false);
          this.eventsError.set(err);
        },
      });
  }
}

