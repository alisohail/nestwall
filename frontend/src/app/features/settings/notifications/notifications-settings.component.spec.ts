import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Observable, of, throwError } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { EmailSettings, EmailSettingsService } from '../email/email-settings.service';
import { NotificationsSettingsComponent } from './notifications-settings.component';
import {
  NotificationSettings,
  NotificationSettingsResponse,
  NotificationsSettingsService,
} from './notifications-settings.service';

const DEFAULT_TEMPLATE = '<html><body>{{body}}</body></html>';

const SAVED: NotificationSettings = {
  in_app: true,
  browser: true,
  email: false,
  email_digest: false,
  digest_minutes: 15,
  email_template: '',
};

describe('NotificationsSettingsComponent', () => {
  let fixture: ComponentFixture<NotificationsSettingsComponent>;
  let api: jasmine.SpyObj<NotificationsSettingsService>;
  let email: jasmine.SpyObj<EmailSettingsService>;

  function setup(perms: string[], smtpReady = true, settings: NotificationSettings = SAVED): void {
    const response: NotificationSettingsResponse = { settings, default_template: DEFAULT_TEMPLATE };
    api = jasmine.createSpyObj<NotificationsSettingsService>('NotificationsSettingsService', ['get', 'save', 'preview']);
    api.get.and.returnValue(of(response));
    api.save.and.callFake((body: NotificationSettings) => of({ settings: body, default_template: DEFAULT_TEMPLATE }));
    api.preview.and.returnValue(of({ html: '<p>Sample</p>' }));

    email = jasmine.createSpyObj<EmailSettingsService>('EmailSettingsService', ['get']);
    email.get.and.returnValue(of({ ready: smtpReady } as EmailSettings));

    TestBed.configureTestingModule({
      imports: [NotificationsSettingsComponent, getTranslocoTestingModule()],
      providers: [
        { provide: NotificationsSettingsService, useValue: api },
        { provide: EmailSettingsService, useValue: email },
        {
          provide: AuthService,
          useValue: {
            can: (perm: string) => perms.includes(perm),
            loadPermissions: (): Observable<void> => of(undefined),
          },
        },
      ],
    });
    fixture = TestBed.createComponent(NotificationsSettingsComponent);
    fixture.detectChanges();
  }

  function el<T extends Element = HTMLElement>(selector: string): T | null {
    return (fixture.nativeElement as HTMLElement).querySelector(selector) as T | null;
  }

  function toggle(testId: string): void {
    el<HTMLInputElement>(`[data-testid="${testId}"]`)?.click();
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
    api.get.and.returnValue(of({ settings: SAVED, default_template: DEFAULT_TEMPLATE }));
    el<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
    fixture.detectChanges();

    expect(api.get).toHaveBeenCalledTimes(1);
    expect(el('app-load-error')).toBeNull();
  });

  it('loads the four switches from the server', () => {
    setup(['settings.view', 'settings.manage']);

    expect(api.get).toHaveBeenCalled();
    expect(el<HTMLInputElement>('[data-testid="notify-in_app"]')?.checked).toBeTrue();
    expect(el<HTMLInputElement>('[data-testid="notify-browser"]')?.checked).toBeTrue();
    expect(el<HTMLInputElement>('[data-testid="notify-email"]')?.checked).toBeFalse();
    expect(el<HTMLInputElement>('[data-testid="notify-digest"]')?.checked).toBeFalse();
  });

  it('disables email when the mail server is not set up', () => {
    setup(['settings.view', 'settings.manage'], false);

    expect(el<HTMLInputElement>('[data-testid="notify-email"]')?.disabled).toBeTrue();
    expect(el('[data-testid="notify-email-not-ready"]')).not.toBeNull();
  });

  it('disables the digest while email is off', () => {
    setup(['settings.view', 'settings.manage']);

    expect(el<HTMLInputElement>('[data-testid="notify-digest"]')?.disabled).toBeTrue();
  });

  it('disables the digest interval while the digest is off', () => {
    setup(['settings.view', 'settings.manage']);

    expect(el<HTMLInputElement>('[data-testid="notify-digest-minutes"]')?.disabled).toBeTrue();
  });

  it('shows the saved digest interval when the digest is on', () => {
    setup(['settings.view', 'settings.manage'], true, { ...SAVED, email: true, email_digest: true, digest_minutes: 60 });

    const input = el<HTMLInputElement>('[data-testid="notify-digest-minutes"]');
    expect(input?.disabled).toBeFalse();
    expect(input?.value).toBe('60');
  });

  it('saves the digest interval with the rest of the settings', () => {
    setup(['settings.view', 'settings.manage'], true, { ...SAVED, email: true, email_digest: true });

    fixture.componentInstance.setDigestMinutes('30');
    fixture.componentInstance.save();

    expect(api.save).toHaveBeenCalledWith({ ...SAVED, email: true, email_digest: true, digest_minutes: 30 });
  });

  it('rejects a digest interval outside 1 to 1440 minutes', () => {
    setup(['settings.view', 'settings.manage'], true, { ...SAVED, email: true, email_digest: true });

    for (const value of ['0', '1441', '2.5', '']) {
      fixture.componentInstance.setDigestMinutes(value);
      fixture.componentInstance.save();
      expect(api.save).not.toHaveBeenCalled();
      expect(fixture.componentInstance.actionError()).toBe('settings.notifications.error.digest_minutes');
    }
  });

  it('saves the whole family setting in one request', () => {
    setup(['settings.view', 'settings.manage']);

    toggle('notify-browser');
    fixture.componentInstance.save();

    expect(api.save).toHaveBeenCalledWith({ ...SAVED, browser: false });
  });

  it('has nothing to save until something changes', () => {
    setup(['settings.view', 'settings.manage']);

    expect(fixture.componentInstance.isDirty()).toBeFalse();
    toggle('notify-browser');
    expect(fixture.componentInstance.isDirty()).toBeTrue();
  });

  it('is read only for users without settings.manage', () => {
    setup(['settings.view']);

    expect(el<HTMLInputElement>('[data-testid="notify-in_app"]')?.disabled).toBeTrue();
    expect(el<HTMLTextAreaElement>('[data-testid="notify-template"]')?.disabled).toBeTrue();
  });

  it('shows the SMTP error when saving email without a mail server', () => {
    setup(['settings.view', 'settings.manage'], true);
    api.save.and.returnValue(throwError(() => ({ code: 'smtp_not_ready', status: 422 }) as ApiError));

    toggle('notify-browser');
    fixture.componentInstance.save();

    expect(fixture.componentInstance.actionError()).toBe('settings.notifications.error.smtp');
  });

  it('rejects a template without the body placeholder', () => {
    setup(['settings.view', 'settings.manage']);

    fixture.componentInstance.setTemplate('<html>no placeholder</html>');
    fixture.componentInstance.preview();

    expect(api.preview).not.toHaveBeenCalled();
    expect(fixture.componentInstance.templateError()).toBe('settings.notifications.error.body');
  });

  it('previews the draft template through the server', () => {
    setup(['settings.view', 'settings.manage']);

    fixture.componentInstance.setTemplate('<p>{{body}}</p>');
    fixture.componentInstance.preview();

    expect(api.preview).toHaveBeenCalledWith('<p>{{body}}</p>');
    expect(fixture.componentInstance.previewHtml()).not.toBeNull();
  });

  it('restores the default template on reset', () => {
    setup(['settings.view', 'settings.manage']);

    fixture.componentInstance.setTemplate('<p>{{body}}</p>');
    fixture.componentInstance.resetTemplate();

    expect(fixture.componentInstance.draft().email_template).toBe(DEFAULT_TEMPLATE);
  });
});
