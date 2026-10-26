import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
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
import {
  API_TOKEN_EXPIRY_DAYS,
  API_TOKEN_SCOPES,
  ApiTokenCreated,
  ApiTokenScope,
  ApiTokenView,
  ApiTokensService,
} from './api-tokens.service';

/** Same limit as the server: names up to 60 characters. */
const MAX_NAME_LENGTH = 60;
const DEFAULT_SCOPES: ApiTokenScope[] = ['tasks:read', 'calendar:read'];
const DEFAULT_EXPIRY_DAYS = 365;

const RELATIVE = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
const RELATIVE_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 864e5],
  ['month', 30 * 864e5],
  ['day', 864e5],
  ['hour', 36e5],
  ['minute', 6e4],
];

/** "3 minutes ago" or "In 11 months", matching the mock. */
function relativeTime(iso: string, now: number): string {
  const diff = Date.parse(iso) - now;
  const unit = RELATIVE_UNITS.find(([, ms]) => Math.abs(diff) >= ms) ?? ['minute', 6e4];
  const text = RELATIVE.format(Math.round(diff / unit[1]), unit[0]);
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** API tokens: list with scopes, last used and expiry, create with scopes and a lifetime, show the token once, and revoke. */
@Component({
  selector: 'app-integrations-settings',
  standalone: true,
  imports: [FormsModule, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent],
  templateUrl: './integrations-settings.component.html',
  styleUrl: './integrations-settings.component.scss',
})
export class IntegrationsSettingsComponent {
  private tokensApi = inject(ApiTokensService);
  private auth = inject(AuthService);
  private destroyRef = inject(DestroyRef);

  readonly scopes = API_TOKEN_SCOPES;
  readonly expiryOptions = API_TOKEN_EXPIRY_DAYS;
  readonly nameMax = MAX_NAME_LENGTH;

  /** Home Assistant REST sensor. The host is the address the browser uses, so it matches this NestWall. */
  readonly homeAssistantYaml = [
    'rest:',
    `  - resource: ${window.location.origin}/api/v1/integrations/summary`,
    '    scan_interval: 300',
    '    headers:',
    '      Authorization: !secret nestwall_bearer',
    '    sensor:',
    '      - name: "NestWall tasks due today"',
    '        value_template: "{{ value_json.data.tasks_due_today }}"',
  ].join('\n');

  /** Grants load asynchronously, so the page waits for them before it decides what to show. */
  permsReady = signal(false);
  canView = signal(false);
  canManage = signal(false);

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<string | null>(null);
  loadApiError = signal<ApiError | null>(null);
  tokens = signal<ApiTokenView[]>([]);

  /** Name of the token that was just revoked, shown in the status line. */
  notice = signal<string | null>(null);
  actionError = signal<string | null>(null);
  confirmingRevokeId = signal<string | null>(null);
  revokingId = signal<string | null>(null);

  // Create dialog
  modalOpen = signal(false);
  name = signal('');
  selectedScopes = signal<ApiTokenScope[]>([...DEFAULT_SCOPES]);
  expiryDays = signal<number>(DEFAULT_EXPIRY_DAYS);
  creating = signal(false);
  modalError = signal<string | null>(null);
  created = signal<ApiTokenCreated | null>(null);
  copied = signal(false);

  nameValid = computed(() => {
    const length = this.name().trim().length;
    return length >= 1 && length <= MAX_NAME_LENGTH;
  });
  canSubmit = computed(() => this.nameValid() && this.selectedScopes().length > 0 && !this.creating());

  constructor() {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.canView.set(this.auth.can('settings.view'));
        this.canManage.set(this.auth.can('settings.manage'));
        this.permsReady.set(true);
        if (this.canView()) this.load();
      });
  }

  load(): void {
    if (!this.canView()) return;
    this.loadState.set('loading');
    this.loadError.set(null);
    this.loadApiError.set(null);
    this.tokensApi
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (tokens) => {
          this.tokens.set(tokens);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(settingsErrorKey(err));
          this.loadApiError.set(err);
          this.loadState.set('error');
        },
      });
  }

  lastUsedLabel(token: ApiTokenView): string | null {
    return token.last_used_at ? relativeTime(token.last_used_at, Date.now()) : null;
  }

  expiresLabel(token: ApiTokenView): string {
    return relativeTime(token.expires_at, Date.now());
  }

  openNew(): void {
    this.name.set('');
    this.selectedScopes.set([...DEFAULT_SCOPES]);
    this.expiryDays.set(DEFAULT_EXPIRY_DAYS);
    this.modalError.set(null);
    this.created.set(null);
    this.copied.set(false);
    this.modalOpen.set(true);
  }

  closeModal(): void {
    if (this.creating()) return;
    this.modalOpen.set(false);
    this.created.set(null);
  }

  closeOnBackdrop(event: Event): void {
    if (event.target === event.currentTarget) this.closeModal();
  }

  isScopeOn(scope: ApiTokenScope): boolean {
    return this.selectedScopes().includes(scope);
  }

  toggleScope(scope: ApiTokenScope): void {
    const on = this.isScopeOn(scope);
    this.selectedScopes.set(API_TOKEN_SCOPES.filter((s) => (s === scope ? !on : this.isScopeOn(s))));
  }

  create(): void {
    if (!this.canSubmit()) return;
    this.creating.set(true);
    this.modalError.set(null);
    this.tokensApi
      .create({
        name: this.name().trim(),
        scopes: this.selectedScopes(),
        expires_in_days: this.expiryDays(),
      })
      .pipe(
        finalize(() => this.creating.set(false)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (token) => {
          this.created.set(token);
          this.refreshList();
        },
        error: (err: ApiError) => this.modalError.set(settingsErrorKey(err)),
      });
  }

  copyToken(): void {
    const token = this.created()?.token;
    if (!token) return;
    navigator.clipboard.writeText(token).then(
      () => this.copied.set(true),
      () => this.copied.set(false),
    );
  }

  askRevoke(token: ApiTokenView): void {
    this.confirmingRevokeId.set(token.id);
  }

  cancelRevoke(): void {
    this.confirmingRevokeId.set(null);
  }

  revoke(token: ApiTokenView): void {
    this.revokingId.set(token.id);
    this.actionError.set(null);
    this.tokensApi
      .revoke(token.id)
      .pipe(
        finalize(() => this.revokingId.set(null)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: () => {
          this.confirmingRevokeId.set(null);
          this.notice.set(token.name);
          this.tokens.update((list) => list.filter((t) => t.id !== token.id));
        },
        error: (err: ApiError) => this.actionError.set(settingsErrorKey(err)),
      });
  }

  /** Reloads the list quietly after a create, without leaving the dialog. */
  private refreshList(): void {
    this.tokensApi
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({ next: (tokens) => this.tokens.set(tokens), error: () => undefined });
  }
}
