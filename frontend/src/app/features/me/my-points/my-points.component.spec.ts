import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { MyPointsResponse, PointsEntry, PointsEntryPage } from '../../rewards/rewards.models';
import { RewardsService } from '../../rewards/rewards.service';
import { MyPointsComponent } from './my-points.component';

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

const entry: PointsEntry = {
    id: 'e1',
    member_id: 'm2',
    type: 'task_award',
    amount: 10,
    reason: null,
    reward_name: null,
    created_at: '2025-01-07T10:00:00Z',
};

describe('MyPointsComponent', () => {
    let fixture: ComponentFixture<MyPointsComponent>;
    let rewards: MockedObject<RewardsService>;

    function build(): void {
        fixture = TestBed.createComponent(MyPointsComponent);
        fixture.detectChanges();
    }

    function el(selector: string): HTMLElement | null {
        return fixture.nativeElement.querySelector(selector) as HTMLElement | null;
    }

    beforeEach(async () => {
        rewards = mockOf<RewardsService>({
            myPoints: vi.fn().mockName("RewardsService.myPoints"),
            entries: vi.fn().mockName("RewardsService.entries")
        });
        rewards.myPoints.mockReturnValue(of(pointsData));
        rewards.entries.mockReturnValue(of({ entries: [entry], next_cursor: null } as PointsEntryPage));

        await TestBed.configureTestingModule({
            imports: [MyPointsComponent, getTranslocoTestingModule()],
            providers: [provideRouter([]), { provide: RewardsService, useValue: rewards }],
        }).compileComponents();
    });

    it('should show the loading state until data arrives', () => {
        rewards.myPoints.mockReturnValue(NEVER as Observable<MyPointsResponse>);
        build();
        expect(el('[data-testid="me-points-loading"]')).toBeTruthy();
    });

    it('should show this week and the recent entries', () => {
        build();
        expect(el('[data-testid="me-points-total"]')?.textContent).toContain('15');
        expect(el('[data-testid="me-points-total"] .total-label')?.textContent).toContain('this week');
        expect(el('[data-testid="me-points-week"]')?.querySelectorAll('.day').length).toBe(7);
        expect(el('[data-testid="me-points-week"]')?.querySelectorAll('.day.today').length).toBe(1);
        expect(el('[data-testid="me-points-entries"]')?.textContent).toContain('+10');
    });

    it('should show the task and bonus stats and the per-day tasks chart', () => {
        build();
        expect(el('[data-testid="me-stat-done"] .stat-value')?.textContent?.trim()).toBe('3');
        expect(el('[data-testid="me-stat-missed"] .stat-value')?.textContent?.trim()).toBe('1');
        expect(el('[data-testid="me-stat-bonus"] .stat-value')?.textContent?.trim()).toBe('4');
        expect(el('[data-testid="me-points-tasks"]')?.querySelectorAll('.day').length).toBe(7);
        expect(el('[data-testid="me-points-tasks"] .done')).toBeTruthy();
    });

    it('should show the reserved points in the info color when points are reserved', () => {
        build();
        const reserved = el('[data-testid="me-points-reserved"]');
        expect(reserved?.textContent).toContain('30');
        expect(reserved?.classList.contains('reserved')).toBe(true);
    });

    it('should hide the reserved line when nothing is reserved', () => {
        rewards.myPoints.mockReturnValue(of({ ...pointsData, reserved: 0 }));
        build();
        expect(el('[data-testid="me-points-reserved"]')).toBeNull();
    });

    it('should not show the balance or streak, which the profile card shows', () => {
        build();
        expect(el('[data-testid="me-points-balance"]')).toBeNull();
        expect(el('[data-testid="me-points-streak"]')).toBeNull();
    });

    it('should show the empty state when there are no entries', () => {
        rewards.entries.mockReturnValue(of({ entries: [], next_cursor: null } as PointsEntryPage));
        build();
        expect(el('[data-testid="me-points"] .nw-empty-note')).toBeTruthy();
        expect(el('[data-testid="me-points-entries"]')).toBeNull();
    });

    it('should show the error state and retry on click', () => {
        rewards.myPoints.mockReturnValue(throwError(() => ({ code: 'network' })));
        build();
        expect(el('[data-testid="me-points-error"]')).toBeTruthy();

        rewards.myPoints.mockReturnValue(of(pointsData));
        (el('[data-testid="me-points-error"] button') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="me-points-error"]')).toBeNull();
        expect(el('[data-testid="me-points-week"]')).toBeTruthy();
    });
});
