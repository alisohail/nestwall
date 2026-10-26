import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Signal, WritableSignal, computed, signal } from '@angular/core';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AuthService, MeResponse } from '../../core/auth/auth.service';
import { ThemeService } from '../../core/theme/theme.service';
import { AvatarService } from '../../shared/avatar/avatar.service';
import { SessionsService } from '../sessions/sessions.service';
import { RewardsService } from '../rewards/rewards.service';
import { MyPointsResponse, PointsEntryPage } from '../rewards/rewards.models';
import { MyTasksService } from '../my-tasks/my-tasks.service';
import { MyTasksResponse } from '../my-tasks/my-tasks.models';
import { BadgesService } from '../badges/badges.service';
import { MyBadges } from '../badges/badges.models';
import { MeComponent } from './me.component';

const childMe: MeResponse = {
  user: { id: 'u2', user_id: 'ava', email: null, display_name: 'Ava', is_child: true, must_change_password: false },
  family: {
    member_id: 'm2',
    family_id: 'f1',
    family_name: 'Home',
    role_id: 'r-child',
    role_name: 'Child',
    status: 'active',
    colour: '#f59e0b',
    avatar_id: null,
    is_parent: false,
  },
};

function badgesOn(over: Partial<MyBadges> = {}): MyBadges {
  return {
    enabled: true,
    show_on_kid_dashboard: true,
    earned_count: 1,
    total_count: 24,
    groups: [
      {
        group: 'streaks',
        earned: 1,
        total: 1,
        badges: [{ key: 's3', earned: true, earned_at: '2026-10-01T08:00:00Z', progress: 3, target: 3 }],
      },
    ],
    from_parents: [],
    ...over,
  };
}

describe('MeComponent', () => {
  let fixture: ComponentFixture<MeComponent>;
  let me: WritableSignal<MeResponse | null | undefined>;
  let authSpy: {
    currentUser: WritableSignal<MeResponse | null | undefined>;
    isChild: Signal<boolean>;
    checkAuth: jasmine.Spy;
    saveTheme: jasmine.Spy;
  };
  let theme: WritableSignal<string>;
  let themeSpy: { currentTheme: WritableSignal<string>; setTheme: jasmine.Spy };

  beforeEach(async () => {
    me = signal<MeResponse | null | undefined>(childMe);
    authSpy = {
      currentUser: me,
      isChild: computed(() => me()?.user.is_child === true),
      checkAuth: jasmine.createSpy('checkAuth').and.returnValue(of(true)),
      saveTheme: jasmine.createSpy('saveTheme').and.returnValue(of(undefined)),
    };
    theme = signal('light');
    themeSpy = {
      currentTheme: theme,
      setTheme: jasmine.createSpy('setTheme').and.callFake((id: string) => theme.set(id)),
    };

    const sessionsSpy = jasmine.createSpyObj<SessionsService>('SessionsService', [
      'listOwn',
      'listForMember',
      'revokeOwn',
      'revokeOthers',
      'revokeAllForMember',
    ]);
    sessionsSpy.listOwn.and.returnValue(of({ sessions: [] }));

    const avatarSpy = jasmine.createSpyObj<AvatarService>('AvatarService', ['url']);
    avatarSpy.url.and.callFake((id: string, size: 96 | 256 = 96) => `/api/v1/avatars/${id}?size=${size}`);

    const rewardsSpy = jasmine.createSpyObj<RewardsService>('RewardsService', ['myPoints', 'entries']);
    rewardsSpy.myPoints.and.returnValue(
      of({
        balance: 120,
        reserved: 0,
        week: [0, 10, 5, 0, 0, 0, 0],
        tasks_done: [0, 0, 0, 0, 0, 0, 0],
        tasks_missed: [0, 0, 0, 0, 0, 0, 0],
        bonus: [0, 0, 0, 0, 0, 0, 0],
        week_start: '2025-01-06',
        timezone: 'UTC',
      } as MyPointsResponse),
    );
    rewardsSpy.entries.and.returnValue(of({ entries: [], next_cursor: null } as PointsEntryPage));

    const myTasksSpy = jasmine.createSpyObj<MyTasksService>('MyTasksService', ['mine']);
    myTasksSpy.mine.and.returnValue(of({ streak_days: 3 } as MyTasksResponse));

    const badgesSpy = jasmine.createSpyObj<BadgesService>('BadgesService', ['mine']);
    badgesSpy.mine.and.returnValue(of(badgesOn()));

    await TestBed.configureTestingModule({
      imports: [MeComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        { provide: AuthService, useValue: authSpy },
        { provide: SessionsService, useValue: sessionsSpy },
        { provide: AvatarService, useValue: avatarSpy },
        { provide: RewardsService, useValue: rewardsSpy },
        { provide: MyTasksService, useValue: myTasksSpy },
        { provide: BadgesService, useValue: badgesSpy },
        { provide: ThemeService, useValue: themeSpy },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(MeComponent);
    fixture.detectChanges();
  });

  it('should show the picture control to a child for their own picture', () => {
    expect(fixture.nativeElement.querySelector('[data-testid="me-picture"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="me-edit-picture"]')).toBeTruthy();
  });

  it('should open the photo editor from the avatar edit button', () => {
    (fixture.nativeElement.querySelector('[data-testid="me-edit-picture"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="photo-editor"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('#photo-dialog-title')?.textContent?.trim()).toBe('My picture');
  });

  it('should show the my details form to a child', () => {
    expect(fixture.nativeElement.querySelector('[data-testid="me-details"] [data-testid="my-details-form"]')).toBeTruthy();
  });


  it('should hide the picture control when the user has no family membership', () => {
    me.set({ user: childMe.user, family: null });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="me-picture"]')).toBeNull();
  });

  it('should refresh the session after the picture changes', () => {
    const component = fixture.componentInstance;
    component.onPictureChanged();
    expect(authSpy.checkAuth).toHaveBeenCalled();
  });

  it('should title the page Profile for a parent and a child', () => {
    const heading = () => fixture.nativeElement.querySelector('h1')?.textContent?.trim();
    expect(heading()).toBe('Profile');
    me.set({ user: { ...childMe.user, is_child: false }, family: childMe.family });
    fixture.detectChanges();
    expect(heading()).toBe('Profile');
  });

  it('should show the badges block to a child with the earned count and recent tiles', () => {
    expect(fixture.nativeElement.querySelector('[data-testid="me-badges"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="me-badges-count"]')?.textContent).toContain('1');
    expect(fixture.nativeElement.querySelectorAll('[data-testid="me-badges-tiles"] li').length).toBe(1);
    expect(fixture.nativeElement.querySelector('[data-testid="me-badges-all"]')?.getAttribute('href')).toContain('/me/badges');
  });

  it('should hide the badges block from a parent account', () => {
    me.set({ user: { ...childMe.user, is_child: false }, family: childMe.family });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="me-badges"]')).toBeNull();
  });

  it('should show the change password card to a child', () => {
    expect(fixture.nativeElement.querySelector('[data-testid="me-password"] [data-testid="own-password"]')).toBeTruthy();
  });

  it('should show the profile stats in the kid profile card', () => {
    expect(fixture.nativeElement.querySelector('[data-testid="me-picture"] app-me-profile-stats')).toBeTruthy();
  });

  it('should show the family name under the kid name', () => {
    expect(fixture.nativeElement.querySelector('.profile .name')?.textContent).toContain('Ava');
    expect(fixture.nativeElement.querySelector('.profile .small')?.textContent).toContain('Home family');
  });

  it('should show the dark mode switch off in light mode and switch to dark on click', () => {
    const toggle = fixture.nativeElement.querySelector('[data-testid="me-dark-mode"]') as HTMLButtonElement;
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    toggle.click();
    fixture.detectChanges();
    expect(themeSpy.setTheme).toHaveBeenCalledWith('dark');
    expect(authSpy.saveTheme).toHaveBeenCalledWith('dark');
    expect(fixture.nativeElement.querySelector('[data-testid="me-dark-mode"]')?.getAttribute('aria-checked')).toBe('true');
  });

  it('should switch back to light mode when the switch is on', () => {
    theme.set('dark');
    fixture.detectChanges();
    (fixture.nativeElement.querySelector('[data-testid="me-dark-mode"]') as HTMLButtonElement).click();
    expect(themeSpy.setTheme).toHaveBeenCalledWith('light');
    expect(authSpy.saveTheme).toHaveBeenCalledWith('light');
  });

  it('should still show points, badges, share and password to a kid without a family', () => {
    me.set({ user: childMe.user, family: null });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="me-picture"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="me-details"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="me-share"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="me-password"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[data-testid="me-badges"]')).toBeTruthy();
  });

  it('should not show the kid settings card to a parent', () => {
    me.set({ user: { ...childMe.user, is_child: false }, family: childMe.family });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="me-settings"]')).toBeNull();
  });

  it('should show the change password card to a parent', () => {
    me.set({ user: { ...childMe.user, is_child: false }, family: childMe.family });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="me-password"] [data-testid="own-password"]')).toBeTruthy();
  });
});
