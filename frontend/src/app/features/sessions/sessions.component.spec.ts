import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';
import { of, throwError } from 'rxjs';
import { AuthService } from '../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { SessionsComponent } from './sessions.component';
import { SessionView, SessionsService } from './sessions.service';

const current: SessionView = {
  id: 's1',
  user_agent: 'Chrome on Windows',
  created_at: '2026-10-10T08:00:00Z',
  expires_at: '2026-11-10T08:00:00Z',
  current: true,
};

const other: SessionView = {
  id: 's2',
  user_agent: 'Safari on iPhone',
  created_at: '2026-10-09T18:30:00Z',
  expires_at: '2026-11-09T18:30:00Z',
  current: false,
};

describe('SessionsComponent', () => {
  let fixture: ComponentFixture<SessionsComponent>;
  let component: SessionsComponent;
  let sessions: jasmine.SpyObj<SessionsService>;
  let auth: jasmine.SpyObj<AuthService>;
  let router: Router;

  function create(memberId: string | null = null, memberName = ''): void {
    fixture = TestBed.createComponent(SessionsComponent);
    component = fixture.componentInstance;
    component.memberId = memberId;
    component.memberName = memberName;
    fixture.detectChanges();
  }

  function el(testId: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
  }

  function click(testId: string): void {
    el(testId)?.click();
    fixture.detectChanges();
  }

  beforeEach(async () => {
    sessions = jasmine.createSpyObj<SessionsService>('SessionsService', [
      'listOwn',
      'listForMember',
      'revokeOwn',
      'revokeOthers',
      'revokeAllForMember',
    ]);
    sessions.listOwn.and.returnValue(of({ sessions: [current, other] }));
    sessions.listForMember.and.returnValue(of({ sessions: [other] }));
    sessions.revokeOwn.and.returnValue(of(undefined));
    sessions.revokeOthers.and.returnValue(of(undefined));
    sessions.revokeAllForMember.and.returnValue(of(undefined));

    auth = jasmine.createSpyObj<AuthService>('AuthService', ['logout']);
    auth.logout.and.returnValue(of(undefined));

    await TestBed.configureTestingModule({
      imports: [SessionsComponent, getTranslocoTestingModule()],
      providers: [
        { provide: SessionsService, useValue: sessions },
        { provide: AuthService, useValue: auth },
      ],
    }).compileComponents();

    router = TestBed.inject(Router);
    spyOn(router, 'navigate').and.returnValue(Promise.resolve(true));
  });

  it('lists own sessions and omits revoke on the current device', () => {
    create();

    expect(sessions.listOwn).toHaveBeenCalled();
    expect(el('session-s1')?.textContent).toContain('This device');
    expect(el('revoke-s1')).toBeNull();
    expect(el('revoke-s2')).not.toBeNull();
  });

  it('removes a session row after it is revoked', () => {
    create();

    click('revoke-s2');

    expect(sessions.revokeOwn).toHaveBeenCalledWith('s2');
    expect(el('session-s2')).toBeNull();
    expect(el('session-s1')).not.toBeNull();
  });

  it('shows the empty state when there are no sessions', () => {
    sessions.listOwn.and.returnValue(of({ sessions: [] }));

    create();

    expect(el('sessions-empty')).not.toBeNull();
    expect(el('session-list')).toBeNull();
  });

  it('shows the load error screen and retries the load when sessions cannot be loaded', () => {
    sessions.listOwn.and.returnValue(throwError(() => ({ code: 'network', message: 'offline' })));

    create();

    expect(el('sessions-loading')).toBeNull();
    expect(fixture.nativeElement.querySelector('app-load-error')).not.toBeNull();
    sessions.listOwn.and.returnValue(of({ sessions: [current, other] }));
    click('load-error-retry');
    expect(sessions.listOwn).toHaveBeenCalledTimes(2);
    expect(el('session-s2')).not.toBeNull();
  });

  it('member mode loads the child sessions without per-row revoke or own sign-out', () => {
    create('m2', 'Ava');

    expect(sessions.listForMember).toHaveBeenCalledWith('m2');
    expect(sessions.listOwn).not.toHaveBeenCalled();
    expect(el('session-s2')).not.toBeNull();
    expect(el('revoke-s2')).toBeNull();
    expect(el('sign-out-others')).toBeNull();
    expect(el('sign-out-everywhere')).not.toBeNull();
  });

  it('member sign out everywhere revokes all child sessions after confirmation', () => {
    create('m2', 'Ava');

    click('sign-out-everywhere');
    expect(el('sign-out-confirm')).not.toBeNull();
    expect(sessions.revokeAllForMember).not.toHaveBeenCalled();

    click('confirm-sign-out-everywhere');

    expect(sessions.revokeAllForMember).toHaveBeenCalledWith('m2');
    expect(sessions.listForMember).toHaveBeenCalledTimes(2);
  });

  it('cancelling the confirmation does not revoke anything', () => {
    create('m2', 'Ava');

    click('sign-out-everywhere');
    click('cancel-sign-out-everywhere');

    expect(sessions.revokeAllForMember).not.toHaveBeenCalled();
    expect(el('sign-out-everywhere')).not.toBeNull();
  });

  it('own sign out everywhere revokes other sessions, logs out and goes to login', () => {
    create();

    click('sign-out-everywhere');
    click('confirm-sign-out-everywhere');

    expect(sessions.revokeOthers).toHaveBeenCalled();
    expect(auth.logout).toHaveBeenCalled();
    expect(router.navigate).toHaveBeenCalledWith(['/login']);
  });

  it('sign out other devices keeps the current session and reloads the list', () => {
    create();

    click('sign-out-others');

    expect(sessions.revokeOthers).toHaveBeenCalled();
    expect(auth.logout).not.toHaveBeenCalled();
    expect(sessions.listOwn).toHaveBeenCalledTimes(2);
  });

  it('shows an action error when revoking fails', () => {
    sessions.revokeOwn.and.returnValue(throwError(() => new Error('nope')));
    create();

    click('revoke-s2');

    expect(el('session-s2')).not.toBeNull();
    expect(fixture.nativeElement.querySelector('[role="alert"]')?.textContent).toContain('did not work');
  });
});
