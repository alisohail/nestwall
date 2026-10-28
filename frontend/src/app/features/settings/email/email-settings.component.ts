import { Component, DestroyRef, OnDestroy, OnInit, TemplateRef, ViewChild, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { finalize } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { settingsErrorKey } from '../settings.format';
import { SettingsHeaderService } from '../settings-header.service';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';
import { EmailSecurity, EmailSettings, EmailSettingsRequest, EmailSettingsService } from './email-settings.service';

type Provider = 'gmail' | 'microsoft' | 'custom';
/** keep: leave the saved password. replace: use the typed one. clear: remove it. */
type PasswordAction = 'keep' | 'replace' | 'clear';

interface ProviderPreset {
  host: string;
  port: number;
  security: EmailSecurity;
}

/** Server and port presets. Custom keeps whatever the family typed. */
const PRESETS: Record<Exclude<Provider, 'custom'>, ProviderPreset> = {
  gmail: { host: 'smtp.gmail.com', port: 587, security: 'starttls' },
  microsoft: { host: 'smtp.office365.com', port: 587, security: 'starttls' },
};

/** Picks the provider tab that matches a saved server. Anything else is custom. */
function providerOf(host: string): Provider {
  const h = host.trim().toLowerCase();
  if (h === PRESETS.gmail.host) return 'gmail';
  if (h === PRESETS.microsoft.host) return 'microsoft';
  return 'custom';
}

/** A translation key, or a message from the server that is already readable. Set one of the two. */
interface Message {
  key?: string;
  message?: string;
}

/** Limits match the server (1 to 1000 emails an hour). */
const SEND_LIMIT_MIN = 1;
const SEND_LIMIT_MAX = 1000;
const PORT_MIN = 1;
const PORT_MAX = 65535;

/** Family mail server. Parents set it up; the outbox sends with it once it is ready. */
@Component({
    selector: 'app-email-settings',
    imports: [FormsModule, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent],
    templateUrl: './email-settings.component.html',
    styleUrl: './email-settings.component.scss'
})
export class EmailSettingsComponent implements OnInit, OnDestroy, UnsavedChangesAware {
  private api = inject(EmailSettingsService);
  private auth = inject(AuthService);
  private destroyRef = inject(DestroyRef);
  private header = inject(SettingsHeaderService);
  /** The values as last loaded or saved. Anything else counts as unsaved. */
  private baseline = '';

  @ViewChild('headerActions', { static: true }) private headerActions!: TemplateRef<unknown>;

  readonly providers: readonly Provider[] = ['gmail', 'microsoft', 'custom'];
  readonly sendLimitMin = SEND_LIMIT_MIN;
  readonly sendLimitMax = SEND_LIMIT_MAX;
  readonly portMin = PORT_MIN;
  readonly portMax = PORT_MAX;

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<string | null>(null);
  loadApiError = signal<ApiError | null>(null);
  canManage = signal(false);
  busy = signal(false);
  saved = signal(false);
  saveError = signal<Message | null>(null);
  testing = signal(false);
  testResult = signal<{ tone: 'success' | 'error'; text: Message } | null>(null);

  ready = signal(false);
  provider = signal<Provider>('custom');
  passwordSet = signal(false);
  passwordAction = signal<PasswordAction>('replace');

  host = '';
  port: number | null = 587;
  security: EmailSecurity = 'starttls';
  username = '';
  fromName = '';
  fromAddress = '';
  sendLimit: number | null = 30;
  password = '';

  constructor() {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.canManage.set(this.auth.can('settings.manage'));
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
    this.loadState.set('loading');
    this.loadError.set(null);
    this.loadApiError.set(null);
    this.api
      .get()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (s) => {
          this.fill(s);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(settingsErrorKey(err));
          this.loadApiError.set(err);
          this.loadState.set('error');
        },
      });
  }

  /** Fills the server and port for a provider. Custom leaves the fields as they are. */
  pickProvider(p: Provider): void {
    this.provider.set(p);
    if (p === 'custom') return;
    const preset = PRESETS[p];
    this.host = preset.host;
    this.port = preset.port;
    this.security = preset.security;
    this.saved.set(false);
  }

  replacePassword(): void {
    this.password = '';
    this.passwordAction.set('replace');
  }

  cancelReplace(): void {
    this.password = '';
    this.passwordAction.set(this.passwordSet() ? 'keep' : 'replace');
  }

  removePassword(): void {
    this.password = '';
    this.passwordAction.set('clear');
  }

  save(): void {
    void this.saveChanges();
  }

  hasUnsavedChanges(): boolean {
    return this.loadState() === 'ready' && this.snapshot() !== this.baseline;
  }

  saveChanges(): Promise<boolean> {
    if (this.busy()) return Promise.resolve(false);
    this.saved.set(false);
    this.saveError.set(null);
    this.testResult.set(null);
    this.busy.set(true);

    return new Promise<boolean>((resolve) => {
      this.api
        .save(this.body())
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => this.busy.set(false)),
        )
        .subscribe({
          next: (s) => {
            this.fill(s);
            this.saved.set(true);
            resolve(true);
          },
          error: (err: ApiError) => {
            this.saveError.set(this.saveErrorOf(err));
            resolve(false);
          },
        });
    });
  }

  sendTest(): void {
    if (this.testing()) return;
    this.testResult.set(null);
    this.testing.set(true);
    this.api
      .sendTest()
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.testing.set(false)),
      )
      .subscribe({
        next: () => this.testResult.set({ tone: 'success', text: { key: 'settings.email.test_sent' } }),
        error: (err: ApiError) => this.testResult.set({ tone: 'error', text: this.testErrorOf(err) }),
      });
  }

  private body(): EmailSettingsRequest {
    return {
      host: this.host.trim(),
      port: Number(this.port),
      security: this.security,
      username: this.username.trim(),
      from_address: this.fromAddress.trim(),
      from_name: this.fromName.trim(),
      send_limit_per_hour: Number(this.sendLimit),
      password: this.passwordValue(),
    };
  }

  /** null keeps the saved password, "" clears it, and any other value replaces it. */
  private passwordValue(): string | null {
    switch (this.passwordAction()) {
      case 'keep':
        return null;
      case 'clear':
        return '';
      case 'replace':
        return this.password === '' ? null : this.password;
    }
  }

  private fill(s: EmailSettings): void {
    this.host = s.host;
    this.port = s.port;
    this.security = s.security;
    this.username = s.username;
    this.fromName = s.from_name;
    this.fromAddress = s.from_address;
    this.sendLimit = s.send_limit_per_hour;
    this.password = '';
    this.passwordSet.set(s.password_set);
    this.passwordAction.set(s.password_set ? 'keep' : 'replace');
    this.ready.set(s.ready);
    this.provider.set(providerOf(s.host));
    this.baseline = this.snapshot();
  }

  private snapshot(): string {
    return JSON.stringify([
      this.host,
      this.port,
      this.security,
      this.username,
      this.fromName,
      this.fromAddress,
      this.sendLimit,
      this.password,
      this.passwordAction(),
    ]);
  }

  /** Validation messages come from the server in English, so they are shown as they are. */
  private saveErrorOf(err: ApiError): Message {
    if (err.code === 'validation_failed' && err.message) return { message: err.message };
    return { key: settingsErrorKey(err) };
  }

  private testErrorOf(err: ApiError): Message {
    switch (err.code) {
      case 'smtp_not_ready':
        return { key: 'settings.email.error.smtp_not_ready' };
      case 'no_email':
        return { key: 'settings.email.error.no_email' };
    }
    return err.message ? { message: err.message } : { key: 'settings.email.error.send_failed' };
  }
}
