import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { RedemptionListItem, RedemptionPage, RedeemResult } from '../rewards.models';
import { RewardsService } from '../rewards.service';
import { RewardRequestsComponent } from './reward-requests.component';

function request(over: Partial<RedemptionListItem> = {}): RedemptionListItem {
    return {
        id: 'q1',
        reward_id: 'r1',
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

describe('RewardRequestsComponent', () => {
    let fixture: ComponentFixture<RewardRequestsComponent>;
    let api: MockedObject<RewardsService>;
    let router: Router;
    let isChild: boolean;

    function build(): void {
        fixture = TestBed.createComponent(RewardRequestsComponent);
        fixture.detectChanges();
    }

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    function click(selector: string): void {
        (el(selector) as HTMLButtonElement).click();
        fixture.detectChanges();
    }

    beforeEach(async () => {
        isChild = true;
        api = mockOf<RewardsService>({
            redemptionPage: vi.fn().mockName("RewardsService.redemptionPage"),
            cancel: vi.fn().mockName("RewardsService.cancel")
        });
        api.redemptionPage.mockReturnValue(of({ redemptions: [request()], next_cursor: null } as RedemptionPage));
        api.cancel.mockReturnValue(of({} as RedeemResult));

        await TestBed.configureTestingModule({
            imports: [RewardRequestsComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: RewardsService, useValue: api },
                { provide: AuthService, useValue: { isChild: () => isChild } as unknown as AuthService },
            ],
        }).compileComponents();

        router = TestBed.inject(Router);
    });

    it('should show the first page of requests', () => {
        build();
        expect(api.redemptionPage).toHaveBeenCalledWith({ limit: 10, cursor: undefined });
        expect(el('[data-testid="request-q1"]')?.textContent).toContain('Ice cream');
        expect(el('[data-testid="requests-pager"]')).toBeNull();
    });

    it('should show the loading state until the first page arrives', () => {
        api.redemptionPage.mockReturnValue(NEVER as Observable<RedemptionPage>);
        build();
        expect(el('[data-testid="requests-loading"]')).toBeTruthy();
        expect(el('[data-testid="requests-list"]')).toBeNull();
    });

    it('should show the empty message when there are no requests', () => {
        api.redemptionPage.mockReturnValue(of({ redemptions: [], next_cursor: null } as RedemptionPage));
        build();
        expect(el('[data-testid="requests-empty"]')).toBeTruthy();
    });

    it('should show the error state and retry on click', () => {
        api.redemptionPage.mockReturnValue(throwError(() => ({ code: 'network' })));
        build();
        expect(el('[data-testid="requests-error"]')).toBeTruthy();

        api.redemptionPage.mockReturnValue(of({ redemptions: [request()], next_cursor: null } as RedemptionPage));
        click('[data-testid="requests-error"] button');
        expect(el('[data-testid="requests-error"]')).toBeNull();
        expect(el('[data-testid="request-q1"]')).toBeTruthy();
    });

    it('should page forward and back with the cursor of each page', () => {
        api.redemptionPage.mockImplementation((query) => query.cursor === 'c1'
            ? of({ redemptions: [request({ id: 'q2', reward_name: 'Cinema' })], next_cursor: null } as RedemptionPage)
            : of({ redemptions: [request()], next_cursor: 'c1' } as RedemptionPage));
        build();
        expect(el('[data-testid="requests-prev"]')?.hasAttribute('disabled')).toBe(true);
        expect(el('[data-testid="requests-page"]')?.textContent).toContain('1');

        click('[data-testid="requests-next"]');
        expect(api.redemptionPage).toHaveBeenCalledWith({ limit: 10, cursor: 'c1' });
        expect(el('[data-testid="request-q2"]')).toBeTruthy();
        expect(el('[data-testid="requests-next"]')?.hasAttribute('disabled')).toBe(true);
        expect(el('[data-testid="requests-page"]')?.textContent).toContain('2');

        click('[data-testid="requests-prev"]');
        expect(api.redemptionPage).toHaveBeenCalledWith({ limit: 10, cursor: undefined });
        expect(el('[data-testid="request-q1"]')).toBeTruthy();
    });

    it('should cancel a pending request with its version and reload the page', () => {
        build();
        api.redemptionPage.mockClear();
        fixture.componentInstance.openCancel(request());
        fixture.detectChanges();
        click('[data-testid="cancel-confirm"]');
        expect(api.cancel).toHaveBeenCalledWith('q1', 3);
        expect(api.redemptionPage).toHaveBeenCalledTimes(1);
        expect(el('[data-testid="cancel-dialog"]')).toBeNull();
    });

    it('should send a parent back to the rewards page', () => {
        isChild = false;
        vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
        build();
        expect(router.navigateByUrl).toHaveBeenCalledWith('/rewards');
        expect(api.redemptionPage).not.toHaveBeenCalled();
    });
});
