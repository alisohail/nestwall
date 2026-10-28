import { Component, DestroyRef, OnDestroy, OnInit, TemplateRef, ViewChild, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { catchError, finalize, forkJoin, of } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { EmailSettingsService } from '../email/email-settings.service';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';
import { settingsErrorKey } from '../settings.format';
import { SettingsHeaderService } from '../settings-header.service';
import { NotificationSettings, NotificationsSettingsService, NotificationSettingsResponse } from './notifications-settings.service';

/** The placeholder that the notices are written into. */
const BODY_PLACEHOLDER = '{{body}}';
/** Matches the server limit on the template length. */
const MAX_TEMPLATE_LENGTH = 32 * 1024;

const EMPTY_SETTINGS: NotificationSettings = {
  in_app: true,
  browser: true,
  email: false,
  email_digest: false,
  digest_minutes: 15,
  email_template: '',
};

/** Digest interval bounds. They match the server: one minute to one day. */
const MIN_DIGEST_MINUTES = 1;
const MAX_DIGEST_MINUTES = 1440;

type Switch = 'in_app' | 'browser' | 'email' | 'email_digest';

/** Placeholders shown under the template editor. Written as strings so Angular does not interpolate them. */
const PLACEHOLDERS = ['{{subject}}', '{{body}}', '{{logo_url}}', '{{app_url}}', '{{family_name}}'];

/**
 * Family notification settings: four switches and the email template. Parents change them; the
 * server stores one set for the whole family.
 */
@Component({
    selector: 'app-notifications-settings',
    imports: [TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent],
    templateUrl: './notifications-settings.component.html',
    styleUrl: './notifications-settings.component.scss'
})
export class NotificationsSettingsComponent implements OnInit, OnDestroy, UnsavedChangesAware {
  private api = inject(NotificationsSettingsService);
  private email = inject(EmailSettingsService);
  private auth = inject(AuthService);
  private transloco = inject(TranslocoService);
  private destroyRef = inject(DestroyRef);
  private header = inject(SettingsHeaderService);
  private sanitizer = inject(DomSanitizer);

  @ViewChild('headerActions', { static: true }) private headerActions!: TemplateRef<unknown>;

  readonly placeholders = PLACEHOLDERS;

  permsReady = signal(false);
  canView = signal(false);
  canManage = signal(false);

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<string | null>(null);
  loadApiError = signal<ApiError | null>(null);
  saving = signal(false);
  previewing = signal(false);
  notice = signal<string | null>(null);
  actionError = signal<string | null>(null);
  templateError = signal<string | null>(null);

  /** Email can only be switched on once the family's mail server is set up. */
  smtpReady = signal(false);
  defaultTemplate = signal('');
  saved = signal<NotificationSettings>(EMPTY_SETTINGS);
  draft = signal<NotificationSettings>(EMPTY_SETTINGS);
  previewHtml = signal<SafeHtml | null>(null);

  readonly isDirty = computed(() => JSON.stringify(this.draft()) !== JSON.stringify(this.saved()));
  readonly emailDisabled = computed(() => !this.canManage() || (!this.smtpReady() && !this.draft().email));
  readonly digestDisabled = computed(() => !this.canManage() || !this.draft().email);
  readonly digestIntervalDisabled = computed(() => !this.canManage() || !this.draft().email_digest);
  readonly digestIntervalValid = computed(() => {
    const m = this.draft().digest_minutes;
    return Number.isInteger(m) && m >= MIN_DIGEST_MINUTES && m <= MAX_DIGEST_MINUTES;
  });
  readonly digestMin = MIN_DIGEST_MINUTES;
  readonly digestMax = MAX_DIGEST_MINUTES;

  constructor() {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.canView.set(this.auth.can('settings.view'));
        this.canManage.set(this.auth.can('settings.manage'));
        this.permsReady.set(true);
        this.load();
      });
  }

  ngOnInit(): void {
    this.header.actions.set(this.headerActions);
  }

  ngOnDestroy(): void {
    if (this.header.actions() === this.headerActions) {
      this.header.actions.set(null);
    }
  }

  load(): void {
    if (!this.canView()) {
      this.loadState.set('ready');
      return;
    }
    this.loadState.set('loading');
    this.loadError.set(null);
    this.loadApiError.set(null);
    forkJoin({
      settings: this.api.get(),
      // If the email settings cannot be read, the email switch stays off.
      email: this.email.get().pipe(catchError(() => of(null))),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ settings, email }) => {
          this.applyResponse(settings);
          this.smtpReady.set(email?.ready ?? false);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(settingsErrorKey(err));
          this.loadApiError.set(err);
          this.loadState.set('error');
        },
      });
  }

  setSwitch(key: Switch, on: boolean): void {
    this.draft.update((d) => ({ ...d, [key]: on }));
  }

  /** Takes the raw input text. An empty or partial value becomes NaN, which fails validation. */
  setDigestMinutes(text: string): void {
    this.draft.update((d) => ({ ...d, digest_minutes: text.trim() === '' ? NaN : Number(text) }));
    this.actionError.set(null);
  }

  setTemplate(text: string): void {
    this.draft.update((d) => ({ ...d, email_template: text }));
    this.templateError.set(null);
  }

  resetTemplate(): void {
    this.setTemplate(this.defaultTemplate());
  }

  hasUnsavedChanges(): boolean {
    return this.loadState() === 'ready' && this.isDirty();
  }

  save(): void {
    void this.saveChanges();
  }

  saveChanges(): Promise<boolean> {
    const body = this.draft();
    if (!this.validTemplate(body.email_template)) return Promise.resolve(false);
    if (!this.digestIntervalValid()) {
      this.actionError.set('settings.notifications.error.digest_minutes');
      return Promise.resolve(false);
    }

    this.saving.set(true);
    this.notice.set(null);
    this.actionError.set(null);
    return new Promise<boolean>((resolve) => {
      this.api
        .save(body)
        .pipe(
          finalize(() => this.saving.set(false)),
          takeUntilDestroyed(this.destroyRef),
        )
        .subscribe({
          next: (res) => {
            this.applyResponse(res);
            this.notice.set(this.transloco.translate('settings.notifications.saved'));
            resolve(true);
          },
          error: (err: ApiError) => {
            this.actionError.set(
              err.code === 'smtp_not_ready' ? 'settings.notifications.error.smtp' : settingsErrorKey(err),
            );
            resolve(false);
          },
        });
    });
  }

  /** Renders the draft template with sample notices. The preview runs in a sandboxed frame. */
  preview(): void {
    const template = this.draft().email_template.trim() || this.defaultTemplate();
    if (!this.validTemplate(template)) return;

    this.previewing.set(true);
    this.actionError.set(null);
    this.api
      .preview(template)
      .pipe(
        finalize(() => this.previewing.set(false)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (res) => this.previewHtml.set(this.sanitizer.bypassSecurityTrustHtml(res.html)),
        error: () => this.actionError.set('settings.notifications.error.preview'),
      });
  }

  private applyResponse(res: NotificationSettingsResponse): void {
    this.defaultTemplate.set(res.default_template);
    this.saved.set(res.settings);
    this.draft.set({ ...res.settings });
  }

  /** Returns true when the template may be saved or previewed. Sets templateError otherwise. */
  private validTemplate(template: string): boolean {
    if (template.length > MAX_TEMPLATE_LENGTH) {
      this.templateError.set('settings.notifications.error.long');
      return false;
    }
    if (template.trim() !== '' && !template.includes(BODY_PLACEHOLDER)) {
      this.templateError.set('settings.notifications.error.body');
      return false;
    }
    this.templateError.set(null);
    return true;
  }
}
