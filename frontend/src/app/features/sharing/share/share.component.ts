import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { finalize, map } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../../shared/ui/empty-state/empty-state.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { newIdempotencyKey } from '../../rewards/idempotency-key';
import { ShareNoteKey, ShareRecipient, ShareSummary } from '../sharing.models';
import { SharingService } from '../sharing.service';
import { quickAmounts } from './quick-amounts';

export const SHARE_NOTES: readonly { key: ShareNoteKey; icon: string }[] = [
  { key: 'thank_you', icon: '💛' },
  { key: 'happy_birthday', icon: '🎂' },
  { key: 'good_job', icon: '💪' },
  { key: 'just_because', icon: '🙌' },
];

/** Longest custom note, in characters. Matches the backend limit. */
export const MAX_NOTE_TEXT = 200;

/** Result of the last successful share, kept for the confirmation view. */
interface SentShare {
  name: string;
  amount: number;
  noteKey: ShareNoteKey | null;
  noteText: string | null;
  pending: boolean;
  left: number;
}

/** Codes that mean the share was refused for a reason the child can see and fix. */
const REFUSED_CODES = ['daily_limit_reached', 'below_minimum_balance', 'sharing_disabled'];

/** Child screen to give some points to a sibling. Parents see a short note instead. */
@Component({
  selector: 'app-share',
  standalone: true,
  imports: [
    TranslocoModule,
    RouterLink,
    AvatarComponent,
    BannerComponent,
    EmptyStateComponent,
    LoadErrorComponent,
    LoadingComponent,
  ],
  templateUrl: './share.component.html',
  styleUrl: './share.component.scss',
})
export class ShareComponent {
  auth = inject(AuthService);
  private sharing = inject(SharingService);
  private destroyRef = inject(DestroyRef);
  private route = inject(ActivatedRoute);

  readonly notes = SHARE_NOTES;

  /** Where the page was opened from. Unknown or missing values return to Rewards. */
  private from = toSignal(this.route.queryParamMap.pipe(map((params) => params.get('from'))), { initialValue: null });

  /** Me returns to the Me page and Today returns to Today; every other origin returns to Rewards. */
  readonly backRoute = computed(() => (this.from() === 'me' ? ['/me'] : this.from() === 'home' ? ['/today'] : ['/rewards']));

  /** Fewest points per share, set by the parent. Defaults to 1 until the summary loads. */
  readonly floor = computed(() => this.summary()?.min_per_share ?? 1);

  /** Most points per share, set by the parent. */
  readonly maxShare = computed(() => this.summary()?.max_per_share ?? 0);

  /** Quick amounts spread across the parent's range. */
  readonly quick = computed(() => (this.summary() ? quickAmounts(this.floor(), this.maxShare()) : []));

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);
  summary = signal<ShareSummary | null>(null);
  toId = signal<string | null>(null);
  amountInput = signal(10);
  noteKey = signal<ShareNoteKey>('thank_you');
  /** 'preset' sends one of the four notes. 'own' sends the text the child typed. */
  noteMode = signal<'preset' | 'own'>('preset');
  noteText = signal('');
  busy = signal(false);
  error = signal<string | null>(null);
  sent = signal<SentShare | null>(null);

  /** Largest amount the child may send right now. Zero when nothing can be sent. */
  readonly cap = computed(() => {
    const s = this.summary();
    if (!s || !s.enabled) return 0;
    return Math.max(0, Math.min(s.max_per_share, s.daily_left, s.balance - s.min_balance));
  });

  /** The amount actually sent: at least the parent's fewest, at most what the child may send now. */
  readonly shown = computed(() => Math.min(Math.max(this.amountInput(), this.floor()), this.cap()));

  readonly chosen = computed<ShareRecipient | null>(
    () => this.summary()?.recipients.find((r) => r.member_id === this.toId()) ?? null,
  );

  readonly canSend = computed(() => this.cap() >= this.floor() && this.chosen() !== null);

  readonly left = computed(() => (this.summary()?.balance ?? 0) - this.shown());

  /** Translation key for the reason sharing is not possible, or null when it is. */
  readonly notice = computed<string | null>(() => {
    const s = this.summary();
    if (!s) return null;
    if (!s.enabled) return 'share.off';
    if (s.recipients.length === 0) return 'share.no_siblings';
    if (s.daily_left <= 0) return 'share.daily_done';
    if (this.cap() < this.floor()) return 'share.not_enough';
    return null;
  });

  /** Idempotency key for the in-flight share. Kept across retries, cleared when the form changes. */
  private pendingKey: string | null = null;

  constructor() {
    if (this.auth.isChild()) {
      this.load();
    }
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.sharing
      .summary()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (s) => {
          this.summary.set(s);
          // Bring the typed amount into the parent's range as soon as the range is known.
          this.amountInput.set(this.shown());
          const stillValid = s.recipients.some((r) => r.member_id === this.toId());
          if (!stillValid) this.toId.set(s.recipients[0]?.member_id ?? null);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  pick(memberId: string): void {
    this.toId.set(memberId);
    this.pendingKey = null;
    this.error.set(null);
  }

  setAmount(value: number): void {
    this.amountInput.set(value);
    this.pendingKey = null;
    this.error.set(null);
  }

  step(delta: number): void {
    this.setAmount(this.shown() + delta);
  }

  /** Accepts typed amounts as they are. Empty or non-numeric text waits for blur. */
  typeAmount(value: string): void {
    const n = Math.trunc(Number(value));
    if (value.trim() === '' || !Number.isFinite(n)) return;
    this.setAmount(n);
  }

  /** Clamps the amount to the allowed range when the field loses focus. */
  commitAmount(box: HTMLInputElement): void {
    this.setAmount(this.shown());
    box.value = String(this.shown());
  }

  pickNote(key: ShareNoteKey): void {
    this.noteMode.set('preset');
    this.noteKey.set(key);
    this.pendingKey = null;
  }

  /** Switches to the child's own note. The text box appears below the chips. */
  pickOwnNote(): void {
    this.noteMode.set('own');
    this.pendingKey = null;
  }

  setNoteText(value: string): void {
    this.noteText.set(value);
    this.pendingKey = null;
  }

  noteIcon(key: ShareNoteKey): string {
    return SHARE_NOTES.find((n) => n.key === key)?.icon ?? '';
  }

  send(): void {
    const s = this.summary();
    const to = this.chosen();
    if (!s || !to || !this.canSend() || this.busy()) return;

    const amount = this.shown();
    // Only one note is sent. Empty custom text means no note.
    const preset = this.noteMode() === 'preset' ? this.noteKey() : null;
    const text = this.noteMode() === 'own' ? this.noteText().trim() : '';
    const key = this.pendingKey ?? newIdempotencyKey();
    this.pendingKey = key;

    this.error.set(null);
    this.busy.set(true);
    this.sharing
      .share(
        {
          to_member_id: to.member_id,
          amount,
          ...(preset ? { note_key: preset } : {}),
          ...(text ? { note_text: text } : {}),
        },
        key,
      )
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.busy.set(false)),
      )
      .subscribe({
        next: (res) => {
          this.pendingKey = null;
          if (!res.replayed) {
            this.summary.update((current) =>
              current
                ? {
                    ...current,
                    balance: res.balance,
                    shared_today: current.shared_today + amount,
                    daily_left: current.daily_left - amount,
                  }
                : current,
            );
          }
          this.sent.set({
            name: to.name,
            amount,
            noteKey: preset,
            noteText: text || null,
            pending: res.status === 'pending',
            left: res.balance,
          });
        },
        error: (err: ApiError) => {
          // A network error or server error may have reached the server, so keep the key for a safe retry.
          const retryable = !err.status || err.status >= 500;
          if (!retryable) this.pendingKey = null;
          this.error.set(this.errorKey(err));
          if (REFUSED_CODES.includes(err.code)) this.load();
        },
      });
  }

  private errorKey(err: ApiError): string {
    switch (err.code) {
      case 'daily_limit_reached':
        return 'share.error.daily';
      case 'below_minimum_balance':
        return 'share.error.minimum';
      case 'sharing_disabled':
        return 'share.error.disabled';
      case 'idempotency_conflict':
        return 'share.error.conflict';
      case 'member_not_found':
        return 'share.error.recipient';
      case 'validation_failed':
        if (err.params?.['field'] === 'note_text') return 'share.error.note';
        return err.params?.['field'] === 'to_member_id' ? 'share.error.recipient' : 'share.error.amount';
    }
    if (err.status === 0) return 'share.error.network';
    return 'share.error.generic';
  }
}
