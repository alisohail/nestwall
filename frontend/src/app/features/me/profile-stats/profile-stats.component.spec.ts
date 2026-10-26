import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { MyTasksService } from '../../my-tasks/my-tasks.service';
import { MyTasksResponse } from '../../my-tasks/my-tasks.models';
import { MyPointsResponse } from '../../rewards/rewards.models';
import { RewardsService } from '../../rewards/rewards.service';
import { MeProfileStatsComponent } from './profile-stats.component';

const pointsData: MyPointsResponse = {
  member_id: 'm2',
  balance: 120,
  reserved: 30,
  timezone: 'UTC',
  week_start: '2025-01-06',
  today: 2,
  week: [0, 10, 5, 0, 0, 0, 0],
  tasks_done: [0, 2, 1, 0, 0, 0, 0],
  tasks_missed: [0, 0, 0, 1, 0, 0, 0],
  bonus: [0, 0, 4, 0, 0, 0, 0],
};

describe('MeProfileStatsComponent', () => {
  let fixture: ComponentFixture<MeProfileStatsComponent>;
  let rewards: jasmine.SpyObj<RewardsService>;
  let tasks: jasmine.SpyObj<MyTasksService>;

  function build(): void {
    fixture = TestBed.createComponent(MeProfileStatsComponent);
    fixture.detectChanges();
  }

  function el(selector: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(selector) as HTMLElement | null;
  }

  beforeEach(async () => {
    rewards = jasmine.createSpyObj<RewardsService>('RewardsService', ['myPoints']);
    rewards.myPoints.and.returnValue(of(pointsData));

    tasks = jasmine.createSpyObj<MyTasksService>('MyTasksService', ['mine']);
    tasks.mine.and.returnValue(of({ streak_days: 4 } as MyTasksResponse));

    await TestBed.configureTestingModule({
      imports: [MeProfileStatsComponent, getTranslocoTestingModule()],
      providers: [
        { provide: RewardsService, useValue: rewards },
        { provide: MyTasksService, useValue: tasks },
      ],
    }).compileComponents();
  });

  it('should show the points balance and the streak', () => {
    build();
    expect(el('[data-testid="me-stat-points"]')?.textContent).toContain('120');
    expect(el('[data-testid="me-stat-streak"]')?.textContent).toContain('4');
  });

  it('should show a skeleton while loading', () => {
    rewards.myPoints.and.returnValue(NEVER as Observable<MyPointsResponse>);
    build();
    expect(el('[data-testid="me-profile-stats-loading"]')).not.toBeNull();
    expect(el('[data-testid="me-stat-points"]')).toBeNull();
    expect(el('[data-testid="me-profile-stats-error"]')).toBeNull();
  });

  it('should show an error when the stats fail to load', () => {
    rewards.myPoints.and.returnValue(throwError(() => ({ code: 'network' })));
    build();
    expect(el('[data-testid="me-profile-stats-error"]')).toBeTruthy();
    expect(el('[data-testid="me-stat-points"]')).toBeNull();
  });
});
