import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { mockOf } from '../../testing/mock-of';
import { AuthService } from '../../core/auth/auth.service';
import { FamilyService } from '../family/family.service';
import { MyPointsResponse, PointsEntryPage, RedeemResult, RedemptionListItem, RedemptionPage, Reward, RewardList } from './rewards.models';
import { RewardsComponent } from './rewards.component';
import { RewardsService } from './rewards.service';

function reward(over: Partial<Reward> = {}): Reward {
    return {
        id: 'r1',
        name: 'Movie night',
        description: null,
        icon: '🎬',
        cost: 50,
        status: 'active',
        limit: null,
        approval_required: false,
        available_from: null,
        available_until: null,
        available_days: null,
        eligible_member_ids: [],
        in_window: true,
        archived: false,
        version: 1,
        ...over,
    };
}

function points(balance: number, reserved = 0): MyPointsResponse {
    return {
        member_id: 'm2',
        balance,
        reserved,
        timezone: 'UTC',
        week_start: '2025-01-06',
        today: 0,
        week: [0, 0, 0, 0, 0, 0, 0],
        tasks_done: [0, 0, 0, 0, 0, 0, 0],
        tasks_missed: [0, 0, 0, 0, 0, 0, 0],
        bonus: [0, 0, 0, 0, 0, 0, 0],
    };
}

function request(over: Partial<RedemptionListItem> = {}): RedemptionListItem {
    return {
        id: 'q1',
        reward_id: 'r2',
        member_id: 'm2',
        status: 'pending',
        cost: 30,
        requested_at: '2025-01-06T10:00:00Z',
        decided_at: null,
        decided_by_member_id: null,
        decision_reason: null,
        version: 3,
        reward_name: 'Ice cream',
        reward_icon: '🍦',
        member_name: 'Ava',
        ...over,
    };
}

describe('RewardsComponent', () => {
    let fixture: ComponentFixture<RewardsComponent>;
    let api: MockedObject<RewardsService>;
    let isChild: boolean;

    function build(): void {
        fixture = TestBed.createComponent(RewardsComponent);
        fixture.detectChanges();
    }

    /** Serves the requests the way the API does: pending requests all at once, otherwise the newest rows up to the limit. */
    function mockRedemptions(all: RedemptionListItem[]): void {
        api.redemptionPage.mockImplementation((query) => {
            const rows = query.status === 'pending' ? all.filter((r) => r.status === 'pending') : all;
            return of({ redemptions: rows.slice(0, query.limit ?? 20), next_cursor: null } as RedemptionPage);
        });
    }

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    beforeEach(async () => {
        isChild = true;
        api = mockOf<RewardsService>({
            list: vi.fn().mockName("RewardsService.list"),
            myPoints: vi.fn().mockName("RewardsService.myPoints"),
            entries: vi.fn().mockName("RewardsService.entries"),
            redemptionPage: vi.fn().mockName("RewardsService.redemptionPage"),
            redeem: vi.fn().mockName("RewardsService.redeem"),
            cancel: vi.fn().mockName("RewardsService.cancel"),
            settings: vi.fn().mockName("RewardsService.settings")
        });
        api.list.mockReturnValue(of({ rewards: [reward()] } as RewardList));
        api.myPoints.mockReturnValue(of(points(60)));
        mockRedemptions([]);
        api.entries.mockReturnValue(of({ entries: [], next_cursor: null } as PointsEntryPage));
        api.redeem.mockReturnValue(of({} as RedeemResult));
        api.cancel.mockReturnValue(of({} as RedeemResult));
        api.settings.mockReturnValue(of({ approval_required_default: false }));

        await TestBed.configureTestingModule({
            imports: [RewardsComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: RewardsService, useValue: api },
                { provide: AuthService, useValue: { isChild: () => isChild } as unknown as AuthService },
                { provide: FamilyService, useValue: { listMembers: () => of({ members: [] }) } as unknown as FamilyService },
            ],
        }).compileComponents();
    });

    it('should show the loading state until data arrives', () => {
        api.list.mockReturnValue(NEVER as Observable<RewardList>);
        build();
        expect(el('[data-testid="rewards-loading"]')).toBeTruthy();
        expect(el('[data-testid="rewards-list"]')).toBeNull();
    });

    it('should list rewards with a request button for a child', () => {
        build();
        expect(el('[data-testid="rewards-kid"]')).toBeTruthy();
        expect(el('[data-testid="reward-r1"]')).toBeTruthy();
        expect(el('[data-testid="reward-request"]')).toBeTruthy();
        expect(el('[data-testid="rewards-balance"]')?.textContent).toContain('60');
    });

    it('should show the error state and retry on click', () => {
        api.list.mockReturnValue(throwError(() => ({ code: 'network' })));
        build();
        expect(el('[data-testid="rewards-error"]')).toBeTruthy();

        api.list.mockReturnValue(of({ rewards: [reward()] } as RewardList));
        (el('[data-testid="rewards-error"] button') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="rewards-error"]')).toBeNull();
        expect(el('[data-testid="reward-r1"]')).toBeTruthy();
    });

    it('should show the empty state when there are no rewards', () => {
        api.list.mockReturnValue(of({ rewards: [] } as RewardList));
        build();
        expect(el('[data-testid="rewards-empty"]')).toBeTruthy();
    });

    it('should show the empty filter message when no reward matches', () => {
        build();
        (el('[data-testid="filter-now"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        // Balance 60 can afford the 50-point reward, so the filter keeps it.
        expect(el('[data-testid="reward-r1"]')).toBeTruthy();

        (el('[data-testid="filter-saving"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="rewards-empty-filter"]')).toBeTruthy();
    });

    it('should keep a requested reward under can get now with its requested badge', () => {
        // Balance 58 cannot cover the 60-point reward, but its points are held by the request.
        api.list.mockReturnValue(of({ rewards: [reward({ id: 'r2', name: 'Screen time', cost: 60 })] } as RewardList));
        api.myPoints.mockReturnValue(of(points(58)));
        mockRedemptions([request({ reward_id: 'r2' })]);
        build();

        (el('[data-testid="filter-now"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="reward-r2"]')).toBeTruthy();
        expect(el('[data-testid="reward-r2"]')?.textContent).toContain('Requested');

        (el('[data-testid="filter-saving"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="reward-r2"]')).toBeNull();
        expect(el('[data-testid="rewards-empty-filter"]')).toBeTruthy();
    });

    it('should keep the requested badge for an older pending request outside the recent list', () => {
        api.list.mockReturnValue(of({ rewards: [reward({ id: 'r2', name: 'Screen time', cost: 60 })] } as RewardList));
        api.myPoints.mockReturnValue(of(points(58)));
        mockRedemptions([
            request({ id: 'a1', reward_id: 'r9', status: 'approved' }),
            request({ id: 'a2', reward_id: 'r9', status: 'approved' }),
            request({ id: 'a3', reward_id: 'r9', status: 'cancelled' }),
            request({ id: 'p1', reward_id: 'r2', status: 'pending' }),
        ]);
        build();
        expect(api.redemptionPage).toHaveBeenCalledWith({ limit: 3 });
        expect(el('[data-testid="request-p1"]')).toBeNull();

        (el('[data-testid="filter-now"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="reward-r2"]')?.textContent).toContain('Requested');
    });

    it('should link to all requests when the child has requests', () => {
        mockRedemptions([request()]);
        build();
        expect(el('[data-testid="rewards-requests-all"]')?.getAttribute('href')).toBe('/rewards/requests');
    });

    it('should not show the all requests link without requests', () => {
        build();
        expect(el('[data-testid="rewards-requests-all"]')).toBeNull();
    });

    it('should cancel a pending request with its version', () => {
        mockRedemptions([request()]);
        build();
        fixture.componentInstance.openCancel(request());
        fixture.componentInstance.confirmCancel();
        expect(api.cancel).toHaveBeenCalledWith('q1', 3);
    });

    it('should show the parent catalogue and not the child view', () => {
        isChild = false;
        build();
        expect(el('[data-testid="rewards-admin"]')).toBeTruthy();
        expect(el('[data-testid="admin-reward-r1"]')).toBeTruthy();
        expect(el('[data-testid="rewards-kid"]')).toBeNull();
        expect(api.list).toHaveBeenCalled();
        expect(api.myPoints).not.toHaveBeenCalled();
    });
});
