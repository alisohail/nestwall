import { Component, DestroyRef, OnInit, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { forkJoin } from 'rxjs';
import { SkeletonComponent } from '../../../shared/ui/skeleton/skeleton.component';
import { MyTasksService } from '../../my-tasks/my-tasks.service';
import { RewardsService } from '../../rewards/rewards.service';

type LoadState = 'loading' | 'ready' | 'error';

export interface ProfileStats {
  balance: number;
  streak: number;
}

/** Points and streak tiles for the child's profile card on the Me page. */
@Component({
    selector: 'app-me-profile-stats',
    imports: [TranslocoModule, SkeletonComponent],
    templateUrl: './profile-stats.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './profile-stats.component.scss'
})
export class MeProfileStatsComponent implements OnInit {
  private rewards = inject(RewardsService);
  private myTasks = inject(MyTasksService);
  private destroyRef = inject(DestroyRef);

  loadState = signal<LoadState>('loading');
  stats = signal<ProfileStats | null>(null);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    forkJoin({
      points: this.rewards.myPoints(),
      tasks: this.myTasks.mine(),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ points, tasks }) => {
          this.stats.set({ balance: points.balance, streak: tasks.streak_days });
          this.loadState.set('ready');
        },
        error: () => this.loadState.set('error'),
      });
  }
}
