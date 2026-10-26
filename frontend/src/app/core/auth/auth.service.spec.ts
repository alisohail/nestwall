import { TestBed } from '@angular/core/testing';
import { of } from 'rxjs';
import { ApiClient } from '../api/api-client.service';
import { AuthService, MeResponse } from './auth.service';

describe('AuthService email prompt', () => {
  let service: AuthService;
  let api: jasmine.SpyObj<ApiClient>;

  function me(isParent: boolean, email: string | null): MeResponse {
    return {
      user: { id: 'u1', user_id: 'sam', email, display_name: 'Sam', is_child: false, must_change_password: false },
      family: { member_id: 'm1', family_id: 'f1', family_name: 'Rivera', role_id: 'r', role_name: 'R', status: 'active', colour: '#000', avatar_id: null, is_parent: isParent },
    };
  }

  beforeEach(() => {
    api = jasmine.createSpyObj<ApiClient>('ApiClient', ['get', 'post']);
    api.post.and.returnValue(of({ user: me(true, null).user }));
    TestBed.configureTestingModule({ providers: [{ provide: ApiClient, useValue: api }] });
    service = TestBed.inject(AuthService);
  });

  it('should ask a parent without an email after login', () => {
    api.get.and.returnValue(of(me(true, null)));
    service.login({ user_id: 'sam', password: 'x' }).subscribe();
    expect(service.emailPromptPending()).toBeTrue();
  });

  it('should not ask a parent who already has an email', () => {
    api.get.and.returnValue(of(me(true, 'sam@rivera.home')));
    service.login({ user_id: 'sam', password: 'x' }).subscribe();
    expect(service.emailPromptPending()).toBeFalse();
  });

  it('should not ask a member who is not a parent', () => {
    api.get.and.returnValue(of(me(false, null)));
    service.login({ user_id: 'sam', password: 'x' }).subscribe();
    expect(service.emailPromptPending()).toBeFalse();
  });

  it('should not ask again when the session is restored on refresh', () => {
    api.get.and.returnValue(of(me(true, null)));
    service.checkAuth().subscribe();
    expect(service.emailPromptPending()).toBeFalse();
  });

  it('should clear the prompt when dismissed', () => {
    api.get.and.returnValue(of(me(true, null)));
    service.login({ user_id: 'sam', password: 'x' }).subscribe();
    service.dismissEmailPrompt();
    expect(service.emailPromptPending()).toBeFalse();
  });
});
