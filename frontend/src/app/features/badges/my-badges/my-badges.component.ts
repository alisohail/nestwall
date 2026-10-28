import { Component, DestroyRef, OnInit, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { ApiError } from '../../../core/api/api-client.service';
import { BUILT_IN_BADGE_COUNT, BUILT_IN_EMOJI, MyBadge, MyBadgeGroup, MyBadges } from '../badges.models';
import { BadgesService } from '../badges.service';

type LoadState = 'loading' | 'ready' | 'error';

/** Fallback picture for a built-in key the client does not know yet. */
const FALLBACK_EMOJI = '🏅';

/** The child's own badges page. Mirrors the child badges mock. */
@Component({
    selector: 'app-my-badges',
    imports: [RouterLink, TranslocoModule, LoadErrorComponent, LoadingComponent],
    templateUrl: './my-badges.component.html',
    styleUrl: './my-badges.component.scss'
})
export class MyBadgesComponent implements OnInit {
  private readonly badges = inject(BadgesService);
  private readonly destroyRef = inject(DestroyRef);

  readonly loadState = signal<LoadState>('loading');
  readonly loadError = signal<ApiError | null>(null);
  readonly data = signal<MyBadges | null>(null);
  readonly builtInTotal = BUILT_IN_BADGE_COUNT;

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.badges
      .mine()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (value) => {
          this.data.set(value);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  emojiFor(key: string): string {
    return BUILT_IN_EMOJI[key] ?? FALLBACK_EMOJI;
  }

  /** Progress as a whole percent, capped at 100. */
  percent(badge: MyBadge): number {
    if (badge.target <= 0) {
      return 0;
    }
    return Math.min(100, Math.round((badge.progress * 100) / badge.target));
  }

  trackGroup(_index: number, group: MyBadgeGroup): string {
    return group.group;
  }

  trackBadge(_index: number, badge: MyBadge): string {
    return badge.key;
  }
}
