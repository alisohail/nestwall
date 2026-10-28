import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { BUILT_IN_BADGE_COUNT, BUILT_IN_EMOJI, MyBadges } from '../badges.models';
import { BadgesService } from '../badges.service';

const FALLBACK_EMOJI = '🏅';
const MAX_STACK = 3;

/** What the pill shows: up to three emoji, the earned count and how many more there are. */
export interface PillView {
  earned: number;
  stack: string[];
  more: number;
}

/**
 * Today header pill for a child. Shows only when the family has badges on and
 * the child's kid dashboard showcase is on. Stays hidden while loading or on error.
 */
@Component({
    selector: 'app-my-badges-pill',
    imports: [RouterLink, TranslocoModule],
    templateUrl: './my-badges-pill.component.html',
    styleUrl: './my-badges-pill.component.scss'
})
export class MyBadgesPillComponent implements OnInit {
  private readonly badges = inject(BadgesService);
  private readonly destroyRef = inject(DestroyRef);

  readonly data = signal<MyBadges | null>(null);
  readonly builtInTotal = BUILT_IN_BADGE_COUNT;

  /** Null when the pill should not show at all. */
  readonly view = computed<PillView | null>(() => {
    const d = this.data();
    if (!d || !d.enabled || !d.show_on_kid_dashboard) {
      return null;
    }
    const recent = d.groups
      .flatMap((group) => group.badges)
      .filter((badge) => badge.earned)
      .sort((a, b) => Date.parse(b.earned_at ?? '') - Date.parse(a.earned_at ?? ''))
      .map((badge) => BUILT_IN_EMOJI[badge.key] ?? FALLBACK_EMOJI);
    const custom = d.from_parents.map((award) => award.emoji);
    const stack = [...custom, ...recent].slice(0, MAX_STACK);
    return { earned: d.earned_count, stack, more: Math.max(0, d.earned_count - stack.length) };
  });

  ngOnInit(): void {
    this.badges
      .mine()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (value) => this.data.set(value),
        error: () => this.data.set(null),
      });
  }
}
