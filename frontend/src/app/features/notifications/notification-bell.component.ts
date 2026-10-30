import { Component, DestroyRef, ElementRef, HostListener, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { DatePipe } from '@angular/common';
import { RouterLink } from '@angular/router';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { interval } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { AuthService } from '../../core/auth/auth.service';
import { isNativeShell, startNativeWatch } from '../../core/native/native-shell';
import { PushService } from '../../core/push/push.service';
import { TimeFormatService } from '../../core/time-format/time-format.service';
import { IconComponent, IconName } from '../../shared/ui/icon/icon.component';
import { SkeletonComponent } from '../../shared/ui/skeleton/skeleton.component';

export interface InboxItem {
  id: string;
  kind: string;
  subject_member_id: string | null;
  body: string;
  created_at: string;
  read_at: string | null;
}

interface InboxResponse {
  notifications: InboxItem[];
  unread_count: number;
  browser_enabled: boolean;
}

/** How often the bell looks for new notifications. The live stream is the main path; this is a fallback. */
const POLL_INTERVAL_MS = 60_000;
/** Most browser notifications shown for one refresh, so a burst does not flood the screen. */
const MAX_BROWSER_PER_REFRESH = 3;
/** Server-sent events endpoint that signals when the member's inbox changes. */
const STREAM_URL = '/api/v1/notifications/stream';
const PANEL_WIDTH_PX = 340;
const PANEL_GAP_PX = 8;

export interface PanelPlacement {
  top: number;
  left: number;
  width: number;
}

/**
 * Places the panel under the bell, right-aligned to it, but never past either edge of the viewport.
 * On a phone the bell sits well inside the right edge, so a plain right alignment runs off the left.
 */
export function panelPlacement(bell: { bottom: number; right: number }, viewportWidth: number): PanelPlacement {
  const width = Math.min(PANEL_WIDTH_PX, viewportWidth - 2 * PANEL_GAP_PX);
  const left = Math.min(Math.max(PANEL_GAP_PX, bell.right - width), viewportWidth - width - PANEL_GAP_PX);
  return { top: bell.bottom + PANEL_GAP_PX, left, width };
}

type Tone = 'success' | 'warning' | 'error' | 'primary' | 'neutral';

interface KindMeta {
  icon: IconName;
  tone: Tone;
}

/** Icon and colour for each notification kind. Unknown kinds fall back to a neutral bell. */
const KIND_META: Record<string, KindMeta> = {
  task_submitted: { icon: 'tasks', tone: 'warning' },
  task_approved: { icon: 'check', tone: 'success' },
  task_rejected: { icon: 'close', tone: 'error' },
  bonus_submitted: { icon: 'star', tone: 'warning' },
  bonus_approved: { icon: 'star', tone: 'success' },
  bonus_rejected: { icon: 'close', tone: 'error' },
  redemption_requested: { icon: 'gift', tone: 'warning' },
  redemption_approved: { icon: 'gift', tone: 'success' },
  redemption_rejected: { icon: 'gift', tone: 'error' },
  points_shared: { icon: 'users', tone: 'primary' },
  points_share_pending: { icon: 'users', tone: 'warning' },
  points_share_declined: { icon: 'users', tone: 'error' },
  badge_earned: { icon: 'star', tone: 'success' },
  badge_awarded: { icon: 'star', tone: 'success' },
  backup_failed: { icon: 'download', tone: 'error' },
  backup_stale: { icon: 'clock', tone: 'warning' },
  password_reset_request: { icon: 'lock', tone: 'primary' },
};
const FALLBACK_META: KindMeta = { icon: 'bell', tone: 'neutral' };

/** Header bell: unread count, a short list of recent notifications, and browser alerts for new ones. */
@Component({
    selector: 'app-notification-bell',
    imports: [TranslocoModule, RouterLink, DatePipe, IconComponent, SkeletonComponent],
    templateUrl: './notification-bell.component.html',
    styleUrl: './notification-bell.component.scss',
    changeDetection: ChangeDetectionStrategy.Eager,
    host: { class: 'notification-bell' }
})
export class NotificationBellComponent {
  private api = inject(ApiClient);
  private host = inject(ElementRef<HTMLElement>);
  private destroyRef = inject(DestroyRef);
  private transloco = inject(TranslocoService);
  private auth = inject(AuthService);
  private push = inject(PushService);
  timeFormat = inject(TimeFormatService);

  /** Only users who can view family settings get the link to the notification settings page. */
  canSettings(): boolean {
    return this.auth.can('settings.view');
  }

  /** Kids have no approvals page, so the footer link is for parents only. */
  isChild(): boolean {
    return this.auth.isChild();
  }

  readonly open = signal(false);
  /** Screen position of the open panel, set each time it opens or the window resizes. */
  readonly panelPos = signal<PanelPlacement | null>(null);
  readonly items = signal<InboxItem[]>([]);
  readonly unread = signal(0);
  readonly loading = signal(false);
  readonly error = signal(false);
  /** Browser notification permission, mirrored so the enable button can hide once decided. */
  readonly permission = signal<NotificationPermission | 'unsupported'>(
    typeof Notification === 'undefined' ? 'unsupported' : Notification.permission,
  );

  /** Whether this browser receives Web Push, which shows alerts even when the app is closed. */
  readonly pushSubscribed = signal(false);

  readonly badge = computed(() => (this.unread() > 99 ? '99+' : String(this.unread())));

  /** Ids already seen. Null until the first load, so existing notices do not raise browser alerts. */
  private seen: Set<string> | null = null;
  private browserEnabled = false;

  constructor() {
    this.refresh();
    this.connectStream();
    if (isNativeShell()) startNativeWatch();
    void this.push.isSubscribed().then((subscribed) => this.pushSubscribed.set(subscribed));
    interval(POLL_INTERVAL_MS)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => this.refresh());
  }

  /** Icon and colour for a notification kind. */
  kindMeta(kind: string): KindMeta {
    return KIND_META[kind] ?? FALLBACK_META;
  }

  /** Translation key for a notification kind's title. */
  kindLabelKey(kind: string): string {
    return `shell.notif.kind.${KIND_META[kind] ? kind : 'other'}`;
  }

  /**
   * Refreshes the list as soon as the server reports a change, so new notices appear without a
   * page reload. Opening the stream also refreshes, which covers changes made before it connected.
   */
  private connectStream(): void {
    if (typeof EventSource === 'undefined') return;
    const source = new EventSource(STREAM_URL);
    source.addEventListener('changed', () => this.refresh());
    source.addEventListener('open', () => this.refresh());
    this.destroyRef.onDestroy(() => source.close());
  }

  toggle(): void {
    this.open.update((v) => !v);
    if (this.open()) {
      this.placePanel();
      void this.enableAlerts();
      this.refresh();
    }
  }

  @HostListener('window:resize')
  onResize(): void {
    if (this.open()) this.placePanel();
  }

  private placePanel(): void {
    const rect = this.host.nativeElement.getBoundingClientRect();
    this.panelPos.set(panelPlacement(rect, window.innerWidth));
  }

  close(): void {
    this.open.set(false);
  }

  refresh(): void {
    this.loading.set(true);
    this.api
      .get<InboxResponse>('/api/v1/notifications')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.items.set(res.notifications.slice(0, 8));
          this.unread.set(res.unread_count);
          this.error.set(false);
          this.loading.set(false);
          this.browserEnabled = res.browser_enabled;
          this.alertNew(res.notifications);
        },
        error: () => {
          this.error.set(true);
          this.loading.set(false);
        },
      });
  }

  markRead(item: InboxItem): void {
    if (item.read_at) return;
    this.api
      .post<InboxItem>(`/api/v1/notifications/${item.id}/read`, {}, undefined, undefined, { silent: true })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.items.update((list) =>
            list.map((n) => (n.id === item.id ? { ...n, read_at: new Date().toISOString() } : n)),
          );
          this.unread.update((n) => Math.max(0, n - 1));
        },
      });
  }

  /**
   * Asks for notification permission if undecided, then subscribes this browser to Web Push. Called
   * from a user gesture (opening the bell or the enable button), which browsers require for the prompt.
   */
  async enableAlerts(): Promise<void> {
    if (!this.browserSupported()) return;
    if (Notification.permission === 'default') await Notification.requestPermission();
    this.permission.set(Notification.permission);
    if (Notification.permission !== 'granted') return;
    try {
      this.pushSubscribed.set(await this.push.subscribe());
    } catch {
      this.pushSubscribed.set(false);
    }
  }

  /** Shown until permission is decided, and again when granted but this browser has no push subscription. */
  showEnableAlerts(): boolean {
    if (!this.browserSupported()) return false;
    if (this.permission() === 'default') return true;
    return this.permission() === 'granted' && this.push.supported && !this.pushSubscribed();
  }

  /**
   * Shows a browser alert for unread notices not seen before. The first load only records what
   * is already there, so opening the app does not replay old notices.
   */
  private alertNew(list: InboxItem[]): void {
    const first = this.seen === null;
    const seen = this.seen ?? new Set<string>();
    const fresh = list.filter((n) => !seen.has(n.id) && !n.read_at);
    list.forEach((n) => seen.add(n.id));
    this.seen = seen;
    if (first || !this.browserEnabled || !this.browserSupported()) return;
    // Web Push already shows these alerts, including on phones where new Notification() is unavailable.
    if (this.pushSubscribed()) return;
    if (Notification.permission !== 'granted') return;

    fresh.slice(0, MAX_BROWSER_PER_REFRESH).forEach((n) => {
      new Notification(this.transloco.translate('shell.notif.browser_title'), { body: n.body, tag: n.id });
    });
  }

  private browserSupported(): boolean {
    return typeof Notification !== 'undefined';
  }

  clearAll(): void {
    this.api
      .delete<void>('/api/v1/notifications')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.items.set([]);
          this.unread.set(0);
        },
        error: () => this.error.set(true),
      });
  }

  @HostListener('document:click', ['$event.target'])
  onDocumentClick(target: EventTarget | null): void {
    if (this.open() && target instanceof Node && !this.host.nativeElement.contains(target)) {
      this.close();
    }
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.close();
  }
}
