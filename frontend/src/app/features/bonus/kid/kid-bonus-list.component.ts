import { Component, DestroyRef, OnInit, computed, inject, input, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { clockOptions } from '../../../core/time-format/clock-style';
import { BonusTask } from '../bonus.models';
import { BonusService } from '../bonus.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../../shared/ui/empty-state/empty-state.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';

/** What a child can do with a bonus card. */
export type KidBonusState = 'open' | 'once' | 'mine' | 'taken' | 'done';

/** The state of one bonus card for the signed-in child. */
export function kidBonusState(task: BonusTask): KidBonusState {
  const claim = task.my_claim;
  if (claim?.status === 'claimed') return 'mine';
  if (claim?.status === 'submitted' || claim?.status === 'approved') return 'done';
  if (task.taken_by_other) return 'taken';
  return task.claim_mode === 'first_come' ? 'open' : 'once';
}

/** Label key for the claim mode chip. */
export function kidBonusModeKey(task: BonusTask): string {
  return task.claim_mode === 'first_come' ? 'kidBonus.mode.first_come' : 'kidBonus.mode.everyone_once';
}

/** Label key for a finished card: waiting for a parent or already approved. */
export function kidBonusDoneKey(task: BonusTask): string {
  return task.my_claim?.status === 'approved' ? 'kidBonus.doneApproved' : 'kidBonus.doneWaiting';
}

/** A task that ends at midnight runs to the end of its day. */
export function kidBonusEndsAtMidnight(task: BonusTask): boolean {
  const end = new Date(task.ends_at);
  return end.getHours() === 0 && end.getMinutes() === 0;
}

/** Label key for the end of the card's window. */
export function kidBonusUntilKey(task: BonusTask): string {
  return kidBonusEndsAtMidnight(task) ? 'kidBonus.untilEndOfDay' : 'kidBonus.until';
}

const untilTimeFormat = (): Intl.DateTimeFormat => new Intl.DateTimeFormat(undefined, clockOptions({ hour: 'numeric', minute: '2-digit' }));

export function kidBonusUntilParams(task: BonusTask): Record<string, string> {
  return { time: untilTimeFormat().format(new Date(task.ends_at)) };
}

/** Translation key for a failed bonus action. */
export function kidBonusErrorKey(err: unknown): string {
  const apiError = err as ApiError;
  if (apiError.status === 0) return 'kidBonus.error.network';
  switch (apiError.code) {
    case 'taken':
      return 'kidBonus.error.taken';
    case 'not_open':
    case 'expired':
    case 'cancelled':
      return 'kidBonus.error.closed';
    case 'wrong_state':
    case 'version_conflict':
      return 'kidBonus.error.changed';
    default:
      return apiError.status === 412 ? 'kidBonus.error.changed' : 'kidBonus.error.generic';
  }
}

/** Cards a child can still act on. */
const LIVE_STATES: readonly KidBonusState[] = ['open', 'once', 'mine'];

/** Shows the child's bonus tasks for today. Used as a section on Today and as the Bonus tab on Tasks. */
@Component({
    selector: 'app-kid-bonus-list',
    imports: [RouterLink, TranslocoModule, BannerComponent, EmptyStateComponent, LoadErrorComponent, LoadingComponent],
    templateUrl: './kid-bonus-list.component.html',
    styleUrl: './kid-bonus-list.component.scss'
})
export class KidBonusListComponent implements OnInit {
  private api = inject(BonusService);
  private destroyRef = inject(DestroyRef);

  /** section: heading and count, hidden when there are no tasks. tab: empty message when there are no tasks. */
  readonly mode = input<'section' | 'tab'>('tab');

  /** The tab the cards are opened from, so the detail page's back link returns there. */
  readonly origin = computed(() => (this.mode() === 'tab' ? 'bonus' : 'home'));

  readonly doneKey = kidBonusDoneKey;
  readonly untilKey = kidBonusUntilKey;
  readonly untilParams = kidBonusUntilParams;

  /** Emits how many cards the child can still act on. */
  readonly openCount = output<number>();

  readonly tasks = signal<BonusTask[]>([]);
  readonly loaded = signal(false);
  readonly loadFailed = signal(false);
  readonly loadError = signal<ApiError | null>(null);
  readonly busyId = signal<string | null>(null);
  readonly error = signal<string | null>(null);

  /** Cards that are still listed: open tasks and the child's own live claims. */
  readonly visible = computed(() => this.tasks().filter((task) => this.isListed(task)));

  readonly active = computed(() => this.visible().filter((task) => LIVE_STATES.includes(kidBonusState(task))).length);

  readonly activeParams = computed(() => ({ count: String(this.active()) }));

  /** Section mode shows as its own card on Today, only when there is something to list. */
  readonly inCard = computed(() => this.mode() === 'section' && this.visible().length > 0);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loadError.set(null);
    this.api
      .list('today')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.tasks.set(res.tasks);
          this.loadFailed.set(false);
          this.loaded.set(true);
          this.openCount.emit(this.active());
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadFailed.set(true);
          this.loaded.set(true);
        },
      });
  }

  stateOf(task: BonusTask): KidBonusState {
    return kidBonusState(task);
  }

  grab(task: BonusTask): void {
    this.run(task.id, this.api.claim(task.id));
  }

  giveBack(task: BonusTask): void {
    const claim = task.my_claim;
    if (!claim) return;
    this.run(task.id, this.api.release(task.id, claim.version));
  }

  finish(task: BonusTask): void {
    const claim = task.my_claim;
    if (!claim) return;
    this.run(task.id, this.api.complete(task.id, claim.version));
  }

  retry(): void {
    this.loadFailed.set(false);
    this.load();
  }

  /** A card is listed while it is open or the child holds a live claim on it. */
  private isListed(task: BonusTask): boolean {
    if (task.state === 'open') return true;
    const status = task.my_claim?.status;
    return status === 'claimed' || status === 'submitted' || status === 'approved';
  }

  private run(taskId: string, request: Observable<unknown>): void {
    if (this.busyId() !== null) return;
    this.error.set(null);
    this.busyId.set(taskId);
    request.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      next: () => {
        this.busyId.set(null);
        this.load();
      },
      error: (err: unknown) => {
        this.busyId.set(null);
        this.error.set(kidBonusErrorKey(err));
        // The card may have changed under the child, so show the current state.
        this.load();
      },
    });
  }
}
