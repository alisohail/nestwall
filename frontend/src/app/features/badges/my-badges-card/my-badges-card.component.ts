import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { SkeletonComponent } from '../../../shared/ui/skeleton/skeleton.component';
import { BUILT_IN_BADGE_COUNT, BUILT_IN_EMOJI, MyBadges } from '../badges.models';
import { BadgesService } from '../badges.service';

type LoadState = 'loading' | 'ready' | 'error';

/** One picture on the Me card. Built-in names come from the catalogue, custom names are the parent's text. */
export interface CardTile {
  id: string;
  emoji: string;
  builtInKey: string | null;
  name: string;
}

const FALLBACK_EMOJI = '🏅';
const MAX_TILES = 6;

/** Me block for a child: earned count, up to six recent badges and a link to the full page. */
@Component({
  selector: 'app-my-badges-card',
  standalone: true,
  imports: [RouterLink, TranslocoModule, BannerComponent, SkeletonComponent],
  templateUrl: './my-badges-card.component.html',
  styleUrl: './my-badges-card.component.scss',
})
export class MyBadgesCardComponent implements OnInit {
  private readonly badges = inject(BadgesService);
  private readonly destroyRef = inject(DestroyRef);

  readonly loadState = signal<LoadState>('loading');
  readonly data = signal<MyBadges | null>(null);
  readonly builtInTotal = BUILT_IN_BADGE_COUNT;

  /** Custom badges first, then the most recent built-in badges, capped at six. */
  readonly tiles = computed<CardTile[]>(() => {
    const d = this.data();
    if (!d || !d.enabled) {
      return [];
    }
    const custom: CardTile[] = d.from_parents.map((award) => ({
      id: `custom-${award.id}`,
      emoji: award.emoji,
      builtInKey: null,
      name: award.name,
    }));
    const earned = d.groups
      .flatMap((group) => group.badges)
      .filter((badge) => badge.earned)
      .sort((a, b) => Date.parse(b.earned_at ?? '') - Date.parse(a.earned_at ?? ''))
      .map((badge) => ({
        id: `builtin-${badge.key}`,
        emoji: BUILT_IN_EMOJI[badge.key] ?? FALLBACK_EMOJI,
        builtInKey: badge.key,
        name: '',
      }));
    return [...custom, ...earned].slice(0, MAX_TILES);
  });

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loadState.set('loading');
    this.badges
      .mine()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (value) => {
          this.data.set(value);
          this.loadState.set('ready');
        },
        error: () => this.loadState.set('error'),
      });
  }
}
