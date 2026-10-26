import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Observable, of, throwError } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { EmailSettings, EmailSettingsRequest, EmailSettingsService } from './email-settings.service';
import { EmailSettingsComponent } from './email-settings.component';

const SAVED: EmailSettings = {
  host: 'smtp.gmail.com',
  port: 587,
  security: 'starttls',
  username: 'family@example.com',
  from_address: 'family@example.com',
  from_name: 'NestWall',
  send_limit_per_hour: 30,
  password_set: true,
  ready: true,
};

describe('EmailSettingsComponent', () => {
  let fixture: ComponentFixture<EmailSettingsComponent>;
  let api: jasmine.SpyObj<EmailSettingsService>;

  function setup(perms: string[], settings: EmailSettings = SAVED): void {
    api = jasmine.createSpyObj<EmailSettingsService>('EmailSettingsService', ['get', 'save', 'sendTest']);
    api.get.and.returnValue(of(settings));
    api.save.and.callFake((body: EmailSettingsRequest) =>
      of({
        ...settings,
        ...body,
        password_set: body.password === null ? settings.password_set : body.password !== '',
        ready: true,
      }),
    );
    api.sendTest.and.returnValue(of(undefined));

    TestBed.configureTestingModule({
      imports: [EmailSettingsComponent, getTranslocoTestingModule()],
      providers: [
        { provide: EmailSettingsService, useValue: api },
        {
          provide: AuthService,
          useValue: {
            can: (perm: string) => perms.includes(perm),
            loadPermissions: (): Observable<void> => of(undefined),
          },
        },
      ],
    });
    fixture = TestBed.createComponent(EmailSettingsComponent);
    fixture.detectChanges();
  }

  function el<T extends Element = HTMLElement>(selector: string): T | null {
    return (fixture.nativeElement as HTMLElement).querySelector(selector) as T | null;
  }

  function type(selector: string, value: string): void {
    const input = el<HTMLInputElement>(selector);
    if (!input) throw new Error(`missing ${selector}`);
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  }

  it('shows the load error screen and retry reloads the settings', () => {
    setup(['settings.view', 'settings.manage']);
    api.get.calls.reset();
    api.get.and.returnValue(throwError(() => ({ code: 'network', status: 0 }) as ApiError));
    fixture.componentInstance.load();
    fixture.detectChanges();

    expect(el('app-loading')).toBeNull();
    expect(el('app-load-error')).not.toBeNull();

    api.get.calls.reset();
    api.get.and.returnValue(of(SAVED));
    el<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
    fixture.detectChanges();

    expect(api.get).toHaveBeenCalledTimes(1);
    expect(el('app-load-error')).toBeNull();
  });

  it('shows the saved server with the password hidden and the status ready', async () => {
    setup(['settings.view', 'settings.manage']);
    await fixture.whenStable();
    fixture.detectChanges();

    expect(el<HTMLInputElement>('[data-testid="email-host"]')?.value).toBe('smtp.gmail.com');
    expect(el('[data-testid="email-status"]')?.textContent).toContain('Ready');
    expect(el('[data-testid="email-password-replace"]')).not.toBeNull();
    expect(el('[data-testid="email-password"]')).toBeNull();
    expect(el('[data-testid="email-gmail-steps"]')).not.toBeNull();
  });

  it('shows the Not set up status and a password field when no password is saved', () => {
    setup(['settings.view', 'settings.manage'], { ...SAVED, host: '', from_address: '', password_set: false, ready: false });

    expect(el('[data-testid="email-status"]')?.textContent).toContain('Not set up');
    expect(el('[data-testid="email-password"]')).not.toBeNull();
    expect(el('[data-testid="email-password-replace"]')).toBeNull();
  });

  it('fills the server and port when a provider is picked', async () => {
    setup(['settings.view', 'settings.manage'], { ...SAVED, host: 'mail.home.lan', port: 25, security: 'tls' });

    el<HTMLButtonElement>('[data-testid="email-provider-microsoft"]')?.click();
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();

    expect(el<HTMLInputElement>('[data-testid="email-host"]')?.value).toBe('smtp.office365.com');
    expect(el<HTMLInputElement>('[data-testid="email-port"]')?.value).toBe('587');
    expect(el('[data-testid="email-gmail-steps"]')).toBeNull();
  });

  it('keeps the saved password when it is not replaced', () => {
    setup(['settings.view', 'settings.manage']);

    fixture.componentInstance.save();
    fixture.detectChanges();

    const body = api.save.calls.mostRecent().args[0];
    expect(body.password).toBeNull();
    expect(body.host).toBe('smtp.gmail.com');
    expect(body.send_limit_per_hour).toBe(30);
  });

  it('sends the new password when it is replaced', () => {
    setup(['settings.view', 'settings.manage']);

    el<HTMLButtonElement>('[data-testid="email-password-replace"]')?.click();
    fixture.detectChanges();
    type('[data-testid="email-password"]', 'abcd efgh ijkl mnop');
    fixture.componentInstance.save();
    fixture.detectChanges();

    expect(api.save.calls.mostRecent().args[0].password).toBe('abcd efgh ijkl mnop');
  });

  it('sends an empty password when the saved one is removed', () => {
    setup(['settings.view', 'settings.manage']);

    el<HTMLButtonElement>('[data-testid="email-password-remove"]')?.click();
    fixture.detectChanges();
    fixture.componentInstance.save();
    fixture.detectChanges();

    expect(api.save.calls.mostRecent().args[0].password).toBe('');
  });

  it('shows the server message when a save is rejected', () => {
    setup(['settings.view', 'settings.manage']);
    const err: ApiError = { code: 'validation_failed', status: 400, message: 'Enter a valid sender address' };
    api.save.and.returnValue(throwError(() => err));

    fixture.componentInstance.save();
    fixture.detectChanges();

    expect(fixture.nativeElement.textContent).toContain('Enter a valid sender address');
  });

  it('reports a successful test send', () => {
    setup(['settings.view', 'settings.manage']);

    fixture.componentInstance.sendTest();
    fixture.detectChanges();

    expect(api.sendTest).toHaveBeenCalled();
    expect(el('[data-testid="email-test-result"]')?.textContent).toContain('Check your inbox');
  });

  it('explains a test send that fails', () => {
    setup(['settings.view', 'settings.manage']);
    api.sendTest.and.returnValue(throwError(() => ({ code: 'send_failed', status: 502, message: '535 authentication failed' }) as ApiError));

    fixture.componentInstance.sendTest();
    fixture.detectChanges();

    expect(el('[data-testid="email-test-result"]')?.textContent).toContain('535 authentication failed');
  });

  it('has no separate on/off switch, because email is on once the server and sender are set', async () => {
    setup(['settings.view', 'settings.manage']);
    await fixture.whenStable();
    fixture.detectChanges();

    expect(el('[data-testid="email-enabled"]')).toBeNull();
  });

  it('disables the form and hides save for users who can view but not manage', async () => {
    setup(['settings.view']);
    await fixture.whenStable();
    fixture.detectChanges();

    expect(fixture.componentInstance.canManage()).toBeFalse();
    expect(el<HTMLFieldSetElement>('fieldset')?.disabled).toBeTrue();
  });
});
