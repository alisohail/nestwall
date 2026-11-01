import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { FamilyService } from '../../family/family.service';
import { Reward, RewardList } from '../rewards.models';
import { RewardsService } from '../rewards.service';
import { RewardsAdminComponent } from './rewards-admin.component';

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

describe('RewardsAdminComponent', () => {
    let fixture: ComponentFixture<RewardsAdminComponent>;
    let api: MockedObject<RewardsService>;

    function build(): void {
        fixture = TestBed.createComponent(RewardsAdminComponent);
        fixture.detectChanges();
    }

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    beforeEach(async () => {
        api = mockOf<RewardsService>({
            list: vi.fn().mockName("RewardsService.list"),
            create: vi.fn().mockName("RewardsService.create"),
            update: vi.fn().mockName("RewardsService.update"),
            archive: vi.fn().mockName("RewardsService.archive"),
            settings: vi.fn().mockName("RewardsService.settings")
        });
        api.list.mockReturnValue(of({ rewards: [reward()] } as RewardList));
        api.settings.mockReturnValue(of({ approval_required_default: false }));
        api.create.mockReturnValue(of({} as Reward) as unknown as Observable<Reward>);
        api.update.mockReturnValue(of({} as Reward) as unknown as Observable<Reward>);
        api.archive.mockReturnValue(of({}));

        await TestBed.configureTestingModule({
            imports: [RewardsAdminComponent, getTranslocoTestingModule()],
            providers: [
                { provide: RewardsService, useValue: api },
                { provide: FamilyService, useValue: { listMembers: () => of({ members: [] }) } as unknown as FamilyService },
            ],
        }).compileComponents();
    });

    it('should show the loading state until rewards arrive', () => {
        api.list.mockReturnValue(NEVER as Observable<RewardList>);
        build();
        expect(el('[data-testid="admin-loading"]')).toBeTruthy();
        expect(el('[data-testid="admin-list"]')).toBeNull();
    });

    it('should list rewards with an edit control', () => {
        build();
        expect(el('[data-testid="admin-list"]')).toBeTruthy();
        expect(el('[data-testid="admin-reward-r1"]')).toBeTruthy();
        expect(el('[data-testid="reward-edit"]')).toBeTruthy();
    });

    it('should hide archived rewards from the list', () => {
        api.list.mockReturnValue(of({ rewards: [reward(), reward({ id: 'r2', archived: true })] } as RewardList));
        build();
        expect(el('[data-testid="admin-reward-r1"]')).toBeTruthy();
        expect(el('[data-testid="admin-reward-r2"]')).toBeNull();
    });

    it('should sort rewards by name by default and by points when chosen', () => {
        api.list.mockReturnValue(of({
            rewards: [
                reward({ id: 'r1', name: 'Zebra', cost: 10 }),
                reward({ id: 'r2', name: 'Apple', cost: 90 }),
                reward({ id: 'r3', name: 'Mango', cost: 40 }),
            ],
        } as RewardList));
        build();
        const order = (): (string | null)[] => Array.from(fixture.nativeElement.querySelectorAll('li[data-testid^="admin-reward-"]') as NodeListOf<HTMLElement>).map((li) => li.getAttribute('data-testid'));
        expect(order()).toEqual(['admin-reward-r2', 'admin-reward-r3', 'admin-reward-r1']);

        const select = el<HTMLSelectElement>('[data-testid="admin-sort"]') as HTMLSelectElement;
        select.value = 'points-asc';
        select.dispatchEvent(new Event('change'));
        fixture.detectChanges();
        expect(order()).toEqual(['admin-reward-r1', 'admin-reward-r3', 'admin-reward-r2']);

        select.value = 'points-desc';
        select.dispatchEvent(new Event('change'));
        fixture.detectChanges();
        expect(order()).toEqual(['admin-reward-r2', 'admin-reward-r3', 'admin-reward-r1']);
    });

    it('should show the empty state when there are no rewards', () => {
        api.list.mockReturnValue(of({ rewards: [] } as RewardList));
        build();
        expect(el('[data-testid="admin-empty"]')).toBeTruthy();
    });

    it('should show the error state and retry on click', () => {
        api.list.mockReturnValue(throwError(() => ({ code: 'network' })));
        build();
        expect(el('[data-testid="admin-error"]')).toBeTruthy();

        api.list.mockReturnValue(of({ rewards: [reward()] } as RewardList));
        (el('[data-testid="admin-error"] button') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="admin-error"]')).toBeNull();
        expect(el('[data-testid="admin-reward-r1"]')).toBeTruthy();
    });

    it('should create a reward with the default draft', () => {
        build();
        fixture.componentInstance.openNew();
        fixture.componentInstance.draft.name = 'Ice cream';
        fixture.componentInstance.save();

        expect(api.create).toHaveBeenCalledWith(expect.objectContaining({
            name: 'Ice cream',
            icon: '🎁',
            cost: 10,
            status: 'active',
            approval_required: false,
            limit_period: null,
            limit_count: null,
            available_from: null,
            available_until: null,
            available_days: null,
            eligible_member_ids: [],
        }));
    });

    it('should start a new reward with approval on when the family default is on', () => {
        api.settings.mockReturnValue(of({ approval_required_default: true }));
        build();
        const c = fixture.componentInstance;
        c.openNew();
        expect(c.draft.approval).toBe(true);
        c.draft.name = 'Ice cream';
        c.save();
        expect(api.create).toHaveBeenCalledWith(expect.objectContaining({ approval_required: true }));
    });

    it('should send a monthly limit as month with its count', () => {
        build();
        const c = fixture.componentInstance;
        c.openNew();
        c.draft.name = 'Movie night';
        c.draft.limitPeriod = 'month';
        c.draft.limitCount = 2;
        c.save();
        expect(api.create).toHaveBeenCalledWith(expect.objectContaining({ limit_period: 'month', limit_count: 2 }));
    });

    it('should send a day selection as days and all seven days as null', () => {
        build();
        const c = fixture.componentInstance;
        c.openNew();
        c.draft.name = 'Pizza';
        c.toggleDay('sat');
        c.toggleDay('mon');
        c.save();
        expect(vi.mocked(api.create).mock.lastCall![0].available_days).toEqual(['mon', 'sat']);

        c.openNew();
        c.draft.name = 'Pizza';
        ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'].forEach((d) => c.toggleDay(d));
        c.save();
        expect(vi.mocked(api.create).mock.lastCall![0].available_days).toBeNull();
    });

    it('should update an existing reward with its version', () => {
        api.list.mockReturnValue(of({ rewards: [reward({ version: 4 })] } as RewardList));
        build();
        const c = fixture.componentInstance;
        c.openEdit(reward({ version: 4 }));
        c.draft.name = 'Cinema trip';
        c.save();

        expect(api.update).toHaveBeenCalledWith('r1', expect.objectContaining({ name: 'Cinema trip' }), 4);
        expect(api.create).not.toHaveBeenCalled();
    });

    it('should block save and show the field error when the name is empty', () => {
        build();
        const c = fixture.componentInstance;
        c.openNew();
        c.draft.name = '   ';
        c.save();
        expect(c.formError()).toBe('rewards.admin.error.name');
        expect(api.create).not.toHaveBeenCalled();
    });

    it('should reject a cost outside 1 to 100000', () => {
        build();
        const c = fixture.componentInstance;
        c.openNew();
        c.draft.name = 'Bike';
        c.draft.cost = 0;
        c.save();
        expect(c.formError()).toBe('rewards.admin.error.cost');

        c.draft.cost = 100001;
        c.save();
        expect(c.formError()).toBe('rewards.admin.error.cost');
        expect(api.create).not.toHaveBeenCalled();
    });

    it('should require a limit count when a limit period is set', () => {
        build();
        const c = fixture.componentInstance;
        c.openNew();
        c.draft.name = 'Sweets';
        c.draft.limitPeriod = 'week';
        c.draft.limitCount = 0;
        c.save();
        expect(c.formError()).toBe('rewards.admin.error.limit');
        expect(api.create).not.toHaveBeenCalled();
    });

    it('should reject a start date after the end date', () => {
        build();
        const c = fixture.componentInstance;
        c.openNew();
        c.draft.name = 'Trip';
        c.draft.from = '2025-06-10';
        c.draft.until = '2025-06-01';
        c.save();
        expect(c.formError()).toBe('rewards.admin.error.window');
    });

    it('should reload and show the stale message on version_conflict', () => {
        build();
        const c = fixture.componentInstance;
        c.openEdit(reward({ version: 2 }));
        api.update.mockReturnValue(throwError(() => ({ code: 'version_conflict' })));
        api.list.mockClear();
        c.save();
        expect(c.formError()).toBe('rewards.admin.error.stale');
        expect(api.list).toHaveBeenCalled();
    });

    it('should not offer archive inside the edit form', () => {
        build();
        fixture.componentInstance.openEdit(reward());
        fixture.detectChanges();
        expect(el('[data-testid="reward-archive"]')).toBeNull();
    });

    it('should show a delete control on each card', () => {
        build();
        expect(el('[data-testid="reward-delete"]')).toBeTruthy();
    });

    it('should archive from the card with the reward version after a confirm', () => {
        api.list.mockReturnValue(of({ rewards: [reward({ version: 3 })] } as RewardList));
        build();
        const c = fixture.componentInstance;
        (el('[data-testid="reward-delete"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="reward-delete-dialog"]')).toBeTruthy();
        expect(api.archive).not.toHaveBeenCalled();

        (el('[data-testid="reward-delete-confirm"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(api.archive).toHaveBeenCalledWith('r1', 3);
        expect(c.deleteTarget()).toBeNull();
        expect(c.notice()).toBe('rewards.admin.deleted');
    });

    it('should close the delete dialog without archiving on cancel', () => {
        build();
        const c = fixture.componentInstance;
        c.askDelete(reward());
        fixture.detectChanges();
        (el('[data-testid="reward-delete-cancel"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="reward-delete-dialog"]')).toBeNull();
        expect(api.archive).not.toHaveBeenCalled();
    });

    it('should keep the dialog open and show the error when the delete fails', () => {
        build();
        const c = fixture.componentInstance;
        api.archive.mockReturnValue(throwError(() => ({ code: 'network' })));
        c.askDelete(reward());
        c.confirmDelete();
        fixture.detectChanges();
        expect(c.deleteTarget()).not.toBeNull();
        expect(el('[data-testid="reward-delete-error"]')).toBeTruthy();
        expect(c.notice()).toBeNull();
    });

    it('should close the dialog and reload on a stale delete', () => {
        build();
        const c = fixture.componentInstance;
        api.archive.mockReturnValue(throwError(() => ({ code: 'version_conflict' })));
        api.list.mockClear();
        c.askDelete(reward({ version: 2 }));
        c.confirmDelete();
        expect(c.deleteTarget()).toBeNull();
        expect(api.list).toHaveBeenCalled();
    });
});
