import { provideHttpClient, withXhr } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { AuthService } from '../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { BadgeAward, BadgeConfig, CustomBadge } from '../badges/badges.models';
import { BadgesService } from '../badges/badges.service';
import { FamilyService, MemberDetail } from '../family/family.service';
import { MemberBalancesResponse, PointsChangeResult, PointsEntry, PointsEntryPage, Reward, RewardList } from '../rewards/rewards.models';
import { RewardsService } from '../rewards/rewards.service';
import { PointsComponent } from './points.component';

function entry(over: Partial<PointsEntry> = {}): PointsEntry {
  return {
    id: 'e1',
    member_id: 'm2',
    type: 'bonus',
    amount: 5,
    reason: 'Tidy room',
    reward_name: null,
    created_at: '2025-01-06T10:00:00Z',
    ...over,
  };
}

function reward(over: Partial<Reward> = {}): Reward {
  return {
    id: 'r1',
    name: 'Reward',
    description: null,
    icon: 'star',
    cost: 100,
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

const CHILD: MemberDetail = {
  id: 'm2',
  user_id: 'u2',
  display_name: 'Ava',
  role_id: 'r-child',
  role_name: 'Child',
  email: null,
  date_of_birth: null,
  gender: null,
  status: 'active',
  colour: '#0f766e',
  is_child: true,
  avatar_id: null,
};

const BADGE: CustomBadge = {
  id: 'c1',
  name: 'Tidy hero',
  emoji: '⭐',
  description: null,
  enabled: true,
  version: 1,
  awarded_count: 0,
};

describe('PointsComponent', () => {
  let fixture: ComponentFixture<PointsComponent>;
  let api: jasmine.SpyObj<RewardsService>;
  let badges: jasmine.SpyObj<BadgesService>;
  let isChild: boolean;

  function build(): void {
    fixture = TestBed.createComponent(PointsComponent);
    fixture.detectChanges();
  }

  function el<T extends Element = HTMLElement>(selector: string): T | null {
    return fixture.nativeElement.querySelector(selector) as T | null;
  }

  beforeEach(async () => {
    isChild = false;
    api = jasmine.createSpyObj<RewardsService>('RewardsService', [
      'entryPage',
      'postPoints',
      'reversePenalty',
      'memberBalances',
      'list',
    ]);
    api.memberBalances.and.returnValue(of({ members: [] } as MemberBalancesResponse));
    api.list.and.returnValue(of({ rewards: [] } as RewardList));
    api.entryPage.and.returnValue(
      of({
        entries: [entry(), entry({ id: 'e2', type: 'penalty', amount: -3, reason: 'Late' })],
        next_cursor: null,
      } as PointsEntryPage),
    );
    api.postPoints.and.returnValue(of({ replayed: false } as PointsChangeResult) as unknown as Observable<PointsChangeResult>);
    api.reversePenalty.and.returnValue(of({ replayed: false } as PointsChangeResult) as unknown as Observable<PointsChangeResult>);

    badges = jasmine.createSpyObj<BadgesService>('BadgesService', ['get', 'award']);
    badges.get.and.returnValue(of({ custom: [BADGE] } as unknown as BadgeConfig));
    badges.award.and.returnValue(of({} as BadgeAward));

    await TestBed.configureTestingModule({
      imports: [PointsComponent, getTranslocoTestingModule()],
      providers: [
        provideHttpClient(withXhr()),
        provideHttpClientTesting(),
        { provide: RewardsService, useValue: api },
        { provide: BadgesService, useValue: badges },
        {
          provide: FamilyService,
          useValue: { listMembers: () => of({ members: [CHILD] }) } as unknown as FamilyService,
        },
        { provide: AuthService, useValue: { isChild: () => isChild, currentUser: () => null } as unknown as AuthService },
      ],
    }).compileComponents();
  });

  it('should show the loading state until entries arrive', () => {
    api.entryPage.and.returnValue(NEVER as Observable<PointsEntryPage>);
    build();
    expect(el('[data-testid="ledger-loading"]')).toBeTruthy();
    expect(el('[data-testid="ledger"]')).toBeNull();
  });

  it('should list ledger entries', () => {
    build();
    expect(el('[data-testid="ledger"]')).toBeTruthy();
    expect(el('[data-testid="entry-e1"]')).toBeTruthy();
    expect(el('[data-testid="entry-e2"]')).toBeTruthy();
    expect(el('[data-testid="entry-e2"] .amount')?.textContent).toContain('-3');
    expect(el('[data-testid="entry-e1"] .amount')?.textContent).toContain('+5');
  });

  it('should show the member avatar and name in the Who cell for a parent', () => {
    build();
    const cell = el('[data-testid="entry-e1"] .col-who');
    expect(cell?.querySelector('app-avatar')).toBeTruthy();
    expect(cell?.textContent).toContain('Ava');
  });

  it('should show the empty state when there are no entries', () => {
    api.entryPage.and.returnValue(of({ entries: [], next_cursor: null } as PointsEntryPage));
    build();
    expect(el('[data-testid="ledger-empty"]')).toBeTruthy();
  });

  it('should show the error state and retry on click', () => {
    api.entryPage.and.returnValue(throwError(() => ({ code: 'network' })));
    build();
    expect(el('[data-testid="ledger-error"]')).toBeTruthy();

    api.entryPage.and.returnValue(of({ entries: [entry()], next_cursor: null } as PointsEntryPage));
    (el('[data-testid="ledger-error"] button') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el('[data-testid="ledger-error"]')).toBeNull();
    expect(el('[data-testid="entry-e1"]')).toBeTruthy();
  });

  it('should show member and manual controls to a parent, and only penalties can be reversed', () => {
    build();
    expect(el('[data-testid="points-add"]')).toBeTruthy();
    expect(el('[data-testid="points-award-badge"]')).toBeTruthy();
    expect(el('[data-testid="filter-member-all"]')).toBeTruthy();
    expect(el('[data-testid="entry-e2"] .reverse')).toBeTruthy();
    expect(el('[data-testid="entry-e1"] .reverse')).toBeNull();
  });

  it('should hide member filter, manual form and reverse controls from a child', () => {
    isChild = true;
    build();
    expect(el('[data-testid="points-add"]')).toBeNull();
    expect(el('[data-testid="filter-member-all"]')).toBeNull();
    expect(el('[data-testid="entry-e2"] .reverse')).toBeNull();
    expect(api.entryPage).toHaveBeenCalledWith(jasmine.objectContaining({ member_id: undefined }));
  });

  it('should show each child balance, reserved points and goal to a parent', () => {
    api.memberBalances.and.returnValue(of({ members: [{ member_id: 'm2', balance: 180, reserved: 40 }] }));
    api.list.and.returnValue(of({ rewards: [reward({ id: 'r1', name: 'Movie night', cost: 250 })] }));
    build();
    expect(el('[data-testid="balance-m2"] .balance-value')?.textContent).toContain('180');
    expect(el('[data-testid="balance-m2"] .balance-goal')?.textContent).toContain('72% to Movie night (250)');
    expect(el('[data-testid="balance-m2"] .balance-reserved')?.textContent).toContain('Reserved: 40 (pending)');
    expect(el('[data-testid="balance-m2"] [role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('72');
  });

  it('should aim each child at the cheapest reward out of reach that they can get', () => {
    api.memberBalances.and.returnValue(of({ members: [{ member_id: 'm2', balance: 180, reserved: 0 }] }));
    api.list.and.returnValue(
      of({
        rewards: [
          reward({ id: 'r1', name: 'Pizza', cost: 120 }),
          reward({ id: 'r2', name: 'Cinema', cost: 200, eligible_member_ids: ['m9'] }),
          reward({ id: 'r3', name: 'Movie night', cost: 250 }),
          reward({ id: 'r4', name: 'Old prize', cost: 210, archived: true }),
          reward({ id: 'r5', name: 'Paused', cost: 220, status: 'inactive' }),
        ],
      } as RewardList),
    );
    build();
    expect(el('[data-testid="balance-m2"] .balance-goal')?.textContent).toContain('Movie night (250)');
  });

  it('should say every reward is within reach when the balance covers the catalogue', () => {
    api.memberBalances.and.returnValue(of({ members: [{ member_id: 'm2', balance: 300, reserved: 0 }] }));
    api.list.and.returnValue(of({ rewards: [reward({ id: 'r1', name: 'Pizza', cost: 120 })] }));
    build();
    expect(el('[data-testid="balance-m2"] .balance-goal')?.textContent).toContain('Every reward is within reach');
    expect(el('[data-testid="balance-m2"] [role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('100');
    expect(el('[data-testid="balance-m2"] .balance-reserved')).toBeNull();
  });

  it('should show no balances to a child', () => {
    isChild = true;
    build();
    expect(api.memberBalances).not.toHaveBeenCalled();
    expect(api.list).not.toHaveBeenCalled();
    expect(el('[data-testid="balances"]')).toBeNull();
  });

  it('should show a balances error without hiding the ledger', () => {
    api.memberBalances.and.returnValue(throwError(() => ({ code: 'network' })));
    build();
    expect(el('[data-testid="balances-error"]')).toBeTruthy();
    expect(el('[data-testid="entry-e1"]')).toBeTruthy();
  });

  it('should reload balances after a bonus is recorded', () => {
    build();
    api.memberBalances.calls.reset();
    const c = fixture.componentInstance;
    c.openManual();
    c.manualPoints = 5;
    c.manualReason = 'Tidy room';
    c.submitManual();
    expect(api.memberBalances).toHaveBeenCalledTimes(1);
  });

  it('should reload balances after a penalty is reversed', () => {
    build();
    api.memberBalances.calls.reset();
    const c = fixture.componentInstance;
    c.askReverse(c.entries()[1]);
    c.reverseReason = 'Was my mistake';
    c.confirmReverse();
    expect(api.memberBalances).toHaveBeenCalledTimes(1);
  });

  it('should filter by type and reload', () => {
    build();
    fixture.componentInstance.setType('penalty');
    expect(api.entryPage).toHaveBeenCalledWith(jasmine.objectContaining({ type: 'penalty' }));
  });

  it('should load the next page with its cursor', () => {
    api.entryPage.and.returnValue(of({ entries: [entry()], next_cursor: 'c1' } as PointsEntryPage));
    build();
    expect(el('[data-testid="ledger-more"]')).toBeTruthy();

    api.entryPage.and.returnValue(of({ entries: [entry({ id: 'e9' })], next_cursor: null } as PointsEntryPage));
    (el('[data-testid="ledger-more"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(api.entryPage).toHaveBeenCalledWith(jasmine.objectContaining({ cursor: 'c1' }));
    expect(el('[data-testid="entry-e9"]')).toBeTruthy();
    expect(el('[data-testid="ledger-more"]')).toBeNull();
  });

  it('should post a bonus with an idempotency key and reload', () => {
    build();
    const c = fixture.componentInstance;
    c.openManual();
    c.manualPoints = 5;
    c.manualReason = ' Tidy room ';
    c.submitManual();

    const args = api.postPoints.calls.mostRecent().args;
    expect(args[0]).toBe('m2');
    expect(args[1]).toEqual({ type: 'bonus', points: 5, reason: 'Tidy room' });
    expect(args[2]).toMatch(/^[0-9a-f]{32}$/);
    expect(c.manualOpen()).toBeFalse();
    expect(c.notice()).toBe('points.notice.saved');
  });

  it('should block submit and show the reason error when the reason is empty', () => {
    build();
    const c = fixture.componentInstance;
    c.openManual();
    c.manualReason = '   ';
    c.submitManual();
    expect(api.postPoints).not.toHaveBeenCalled();
    expect(c.manualError()).toBe('points.error.reason');
    expect(c.manualOpen()).toBeTrue();
  });

  it('should reject a zero correction', () => {
    build();
    const c = fixture.componentInstance;
    c.openManual();
    c.setManualType('adjustment');
    c.manualPoints = 0;
    c.manualReason = 'Fix';
    c.submitManual();
    expect(api.postPoints).not.toHaveBeenCalled();
    expect(c.manualError()).toBe('points.error.adjust');
  });

  it('should show the mapped error and keep the form open when the child is gone', () => {
    api.postPoints.and.returnValue(throwError(() => ({ code: 'member_not_found' })));
    build();
    const c = fixture.componentInstance;
    c.openManual();
    c.manualReason = 'Fix';
    c.submitManual();
    expect(c.manualError()).toBe('points.error.memberNotFound');
    expect(c.manualOpen()).toBeTrue();
  });

  it('should show the replay notice when the change was already recorded', () => {
    api.postPoints.and.returnValue(of({ replayed: true } as PointsChangeResult) as unknown as Observable<PointsChangeResult>);
    build();
    const c = fixture.componentInstance;
    c.openManual();
    c.manualReason = 'Fix';
    c.submitManual();
    expect(c.notice()).toBe('points.notice.replayed');
  });

  it('should reverse a penalty with a reason', () => {
    build();
    const c = fixture.componentInstance;
    c.askReverse(entry({ id: 'e2', type: 'penalty', amount: -3 }));
    c.reverseReason = 'Mistake';
    c.confirmReverse();
    expect(api.reversePenalty).toHaveBeenCalledWith('e2', 'Mistake');
    expect(c.reverseTarget()).toBeNull();
    expect(c.notice()).toBe('points.notice.reversed');
  });

  it('should show the mapped error when the penalty was already reversed', () => {
    api.reversePenalty.and.returnValue(throwError(() => ({ code: 'already_reversed' })));
    build();
    const c = fixture.componentInstance;
    c.askReverse(entry({ id: 'e2', type: 'penalty', amount: -3 }));
    c.reverseReason = 'Mistake';
    c.confirmReverse();
    expect(c.reverseError()).toBe('points.error.alreadyReversed');
    expect(c.reverseTarget()).not.toBeNull();
  });

  it('should hide the award button from a child', () => {
    isChild = true;
    build();
    expect(el('[data-testid="points-award-badge"]')).toBeNull();
  });

  it('should load enabled custom badges when the award dialog opens', () => {
    badges.get.and.returnValue(
      of({ custom: [BADGE, { ...BADGE, id: 'c2', enabled: false }] } as unknown as BadgeConfig),
    );
    build();
    const c = fixture.componentInstance;
    c.openAward();
    expect(badges.get).toHaveBeenCalled();
    expect(c.awardOpen()).toBeTrue();
    expect(c.awardBadges().map((b) => b.id)).toEqual(['c1']);
  });

  it('should block an award until child, badge and note are set', () => {
    build();
    const c = fixture.componentInstance;
    c.openAward();
    c.submitAward();
    expect(badges.award).not.toHaveBeenCalled();
    expect(c.awardError()).toBe('settings.badges.error.child');

    c.awardMemberId = 'm2';
    c.submitAward();
    expect(c.awardError()).toBe('settings.badges.error.badge');

    c.awardBadgeId = 'c1';
    c.submitAward();
    expect(c.awardError()).toBe('settings.badges.error.note');
    expect(badges.award).not.toHaveBeenCalled();
    expect(c.awardOpen()).toBeTrue();
  });

  it('should award a badge with a note and an idempotency key, then close', () => {
    build();
    const c = fixture.componentInstance;
    c.openAward();
    c.awardMemberId = 'm2';
    c.awardBadgeId = 'c1';
    c.awardNote = '  Cleaned the garage  ';
    c.submitAward();

    const args = badges.award.calls.mostRecent().args;
    expect(args[0]).toEqual({ member_id: 'm2', badge_id: 'c1', note: 'Cleaned the garage' });
    expect(args[1]).toMatch(/^[0-9a-f]{32}$/);
    expect(c.awardOpen()).toBeFalse();
  });

  it('should show the mapped error and keep the award dialog open when the badge is disabled', () => {
    badges.award.and.returnValue(throwError(() => ({ code: 'badge_disabled' })));
    build();
    const c = fixture.componentInstance;
    c.openAward();
    c.awardMemberId = 'm2';
    c.awardBadgeId = 'c1';
    c.awardNote = 'Nice work';
    c.submitAward();
    expect(c.awardError()).toBe('settings.badges.error.badge_disabled');
    expect(c.awardOpen()).toBeTrue();
  });
});
