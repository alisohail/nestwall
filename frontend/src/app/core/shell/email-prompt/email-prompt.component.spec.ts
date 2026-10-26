import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { signal } from '@angular/core';
import { getTranslocoTestingModule } from '../../transloco/transloco-testing.module';
import { AuthService, MeResponse } from '../../auth/auth.service';
import { EmailPromptComponent } from './email-prompt.component';

describe('EmailPromptComponent', () => {
  let fixture: ComponentFixture<EmailPromptComponent>;
  let pending: ReturnType<typeof signal<boolean>>;
  let dismiss: jasmine.Spy;
  let router: Router;

  const me: MeResponse = {
    user: { id: 'u1', user_id: 'sam', email: null, display_name: 'Sam', is_child: false, must_change_password: false },
    family: { member_id: 'm1', family_id: 'f1', family_name: 'Rivera', role_id: 'r-parent', role_name: 'Parent', status: 'active', colour: '#000', avatar_id: null, is_parent: true },
  };

  function create(isPending: boolean): void {
    pending = signal(isPending);
    dismiss = jasmine.createSpy('dismissEmailPrompt').and.callFake(() => pending.set(false));
    TestBed.configureTestingModule({
      imports: [EmailPromptComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        {
          provide: AuthService,
          useValue: { emailPromptPending: pending, currentUser: signal<MeResponse>(me), dismissEmailPrompt: dismiss },
        },
      ],
    });
    router = TestBed.inject(Router);
    spyOn(router, 'navigate').and.resolveTo(true);
    fixture = TestBed.createComponent(EmailPromptComponent);
    fixture.detectChanges();
  }

  it('should show the prompt when a parent has no email', () => {
    create(true);
    expect(fixture.nativeElement.querySelector('[data-testid="email-prompt"]')).toBeTruthy();
    expect(fixture.nativeElement.querySelector('[role="dialog"]')).toBeTruthy();
  });

  it('should render nothing once the prompt is not pending', () => {
    create(false);
    expect(fixture.nativeElement.querySelector('[data-testid="email-prompt"]')).toBeNull();
  });

  it('should close the prompt with Later', () => {
    create(true);
    fixture.nativeElement.querySelector('[data-testid="email-prompt-later"]').click();
    fixture.detectChanges();
    expect(dismiss).toHaveBeenCalled();
    expect(fixture.nativeElement.querySelector('[data-testid="email-prompt"]')).toBeNull();
    expect(router.navigate).not.toHaveBeenCalled();
  });

  it('should close the prompt with Escape', () => {
    create(true);
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    fixture.detectChanges();
    expect(dismiss).toHaveBeenCalled();
  });

  it('should send Set email to the signed-in parent edit page', () => {
    create(true);
    fixture.nativeElement.querySelector('[data-testid="email-prompt-set"]').click();
    fixture.detectChanges();
    expect(dismiss).toHaveBeenCalled();
    expect(router.navigate).toHaveBeenCalledWith(['/family', 'members', 'm1']);
  });
});
