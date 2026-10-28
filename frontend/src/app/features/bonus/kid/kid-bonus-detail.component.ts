import { Component, DestroyRef, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable, map } from 'rxjs';
import { BonusTask } from '../bonus.models';
import { BonusService } from '../bonus.service';
import { ApiError } from '../../../core/api/api-client.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import {
  KidBonusState,
  kidBonusDoneKey,
  kidBonusErrorKey,
  kidBonusModeKey,
  kidBonusState,
  kidBonusUntilKey,
  kidBonusUntilParams,
} from './kid-bonus-list.component';

type LoadState = 'loading' | 'ready' | 'missing' | 'error';

/** One bonus card for the signed-in child, with the same actions as the list. */
@Component({
    selector: 'app-kid-bonus-detail',
    imports: [RouterLink, TranslocoModule, BannerComponent, LoadErrorComponent, LoadingComponent],
    templateUrl: './kid-bonus-detail.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './kid-bonus-detail.component.scss'
})
export class KidBonusDetailComponent {
  private route = inject(ActivatedRoute);
  private api = inject(BonusService);
  private destroyRef = inject(DestroyRef);

  /** The origin the bonus was opened from. Unknown values fall back to the Bonus tab. */
  readonly backTab = toSignal(
    this.route.queryParamMap.pipe(map((params) => params.get('from') ?? 'bonus')),
    { initialValue: 'bonus' },
  );

  /** The Today page returns to /today; every other origin returns to its My tasks tab. */
  readonly backRoute = computed(() => (this.backTab() === 'home' ? ['/today'] : ['/my-tasks']));
  readonly backQuery = computed(() => (this.backTab() === 'home' ? null : { tab: this.backTab() }));

  readonly task = signal<BonusTask | null>(null);
  readonly loadState = signal<LoadState>('loading');
  readonly loadError = signal<ApiError | null>(null);
  readonly busy = signal(false);
  readonly error = signal<string | null>(null);

  readonly state = computed<KidBonusState | null>(() => {
    const task = this.task();
    return task ? kidBonusState(task) : null;
  });

  readonly modeKey = kidBonusModeKey;
  readonly doneKey = kidBonusDoneKey;
  readonly untilKey = kidBonusUntilKey;
  readonly untilParams = kidBonusUntilParams;

  private currentId: string | null = null;

  constructor() {
    this.route.paramMap.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((params) => {
      this.currentId = params.get('id');
      this.load();
    });
  }

  /** Loads today's bonus list and picks this card out of it. A card that is not on the list shows the not-found state. */
  load(): void {
    const id = this.currentId;
    if (!id) {
      this.loadState.set('missing');
      return;
    }
    this.loadError.set(null);
    this.loadState.set('loading');
    this.reload(id);
  }

  retry(): void {
    this.load();
  }

  grab(): void {
    const task = this.task();
    if (task) this.run(this.api.claim(task.id));
  }

  giveBack(): void {
    const task = this.task();
    if (task?.my_claim) this.run(this.api.release(task.id, task.my_claim.version));
  }

  finish(): void {
    const task = this.task();
    if (task?.my_claim) this.run(this.api.complete(task.id, task.my_claim.version));
  }

  /** Refreshes the card without showing the loading state, so the buttons stay in place. */
  private reload(id: string): void {
    this.api
      .list('today')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          const found = res.tasks.find((task) => task.id === id) ?? null;
          this.task.set(found);
          this.loadState.set(found ? 'ready' : 'missing');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  private run(request: Observable<unknown>): void {
    const id = this.currentId;
    if (this.busy() || !id) return;
    this.error.set(null);
    this.busy.set(true);
    request.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      next: () => {
        this.busy.set(false);
        this.reload(id);
      },
      error: (err: unknown) => {
        this.busy.set(false);
        this.error.set(kidBonusErrorKey(err));
        // The card may have changed under the child, so show the current state.
        this.reload(id);
      },
    });
  }
}
