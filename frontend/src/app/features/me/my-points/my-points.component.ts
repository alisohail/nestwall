import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { forkJoin } from 'rxjs';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { SkeletonComponent } from '../../../shared/ui/skeleton/skeleton.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { MyPointsResponse, PointsEntry } from '../../rewards/rewards.models';
import { RewardsService } from '../../rewards/rewards.service';

type LoadState = 'loading' | 'ready' | 'error';

const DAY_MS = 86_400_000;

function sumOf(values: number[] | undefined): number {
  return (values ?? []).reduce((sum, v) => sum + v, 0);
}

/** Short weekday name for the day index, in the browser language. */
function weekdayLabel(weekStart: string, index: number): string {
  const day = new Date(Date.parse(`${weekStart}T00:00:00Z`) + index * DAY_MS);
  return new Intl.DateTimeFormat(undefined, { weekday: 'short', timeZone: 'UTC' }).format(day);
}

/** This week's points chart and recent entries, shown on the Me page for a child. */
@Component({
  selector: 'app-my-points',
  standalone: true,
  imports: [TranslocoModule, DatePipe, BannerComponent, IconComponent, SkeletonComponent],
  templateUrl: './my-points.component.html',
  styleUrl: './my-points.component.scss',
})
export class MyPointsComponent implements OnInit {
  private rewards = inject(RewardsService);
  private destroyRef = inject(DestroyRef);

  loadState = signal<LoadState>('loading');
  points = signal<MyPointsResponse | null>(null);
  entries = signal<PointsEntry[]>([]);

  days = computed(() => {
    const p = this.points();
    if (!p) return [];
    const max = Math.max(1, ...p.week);
    return p.week.map((value, index) => ({
      value,
      label: weekdayLabel(p.week_start, index),
      percent: Math.round((value / max) * 100),
      today: index === p.today,
    }));
  });

  /** Tasks done and missed per day, with bar heights scaled to the busiest day. */
  taskDays = computed(() => {
    const p = this.points();
    if (!p) return [];
    const done = p.tasks_done;
    const missed = p.tasks_missed;
    const max = Math.max(1, ...done.map((v, i) => v + missed[i]));
    return done.map((value, index) => ({
      done: value,
      missed: missed[index],
      donePercent: Math.round((value / max) * 100),
      missedPercent: Math.round((missed[index] / max) * 100),
      label: weekdayLabel(p.week_start, index),
      today: index === p.today,
    }));
  });

  weekTotal = computed(() => (this.points()?.week ?? []).reduce((sum, v) => sum + v, 0));
  doneTotal = computed(() => sumOf(this.points()?.tasks_done));
  missedTotal = computed(() => sumOf(this.points()?.tasks_missed));
  bonusTotal = computed(() => sumOf(this.points()?.bonus));

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    if (this.loadState() !== 'ready') this.loadState.set('loading');
    forkJoin({
      points: this.rewards.myPoints(),
      entries: this.rewards.entries(5),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ points, entries }) => {
          this.points.set(points);
          this.entries.set(entries.entries);
          this.loadState.set('ready');
        },
        error: () => this.loadState.set('error'),
      });
  }

  sign(amount: number): string {
    return amount > 0 ? `+${amount}` : `${amount}`;
  }
}
