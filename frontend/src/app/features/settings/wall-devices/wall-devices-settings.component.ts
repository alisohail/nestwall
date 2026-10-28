import { Component, DestroyRef, HostListener, OnDestroy, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { DatePipe } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { Subscription, finalize, interval } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { settingsErrorKey } from '../settings.format';
import { WallDeviceCreated, WallDeviceUpdate, WallDeviceView, WallDevicesService } from './wall-devices.service';

/** Same limits as the server: names up to 60 characters, exit codes of 4 to 6 digits. */
const MAX_NAME_LENGTH = 60;
const EXIT_CODE = /^[0-9]{4,6}$/;
/** How often the list refreshes, so status and last seen stay current while the page is open. */
const REFRESH_MS = 30_000;

/** Wall devices: list and status, configure name and exit code, add a device with a pairing key, and revoke. */
@Component({
    selector: 'app-wall-devices-settings',
    imports: [DatePipe, FormsModule, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent],
    templateUrl: './wall-devices-settings.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './wall-devices-settings.component.scss'
})
export class WallDevicesSettingsComponent implements OnDestroy {
  private devicesApi = inject(WallDevicesService);
  private auth = inject(AuthService);
  private transloco = inject(TranslocoService);
  private destroyRef = inject(DestroyRef);

  /** The address the tablet opens to enter the key. */
  readonly pairHost = window.location.host;

  /** Grants load asynchronously, so the page waits for them before it decides what to show. */
  permsReady = signal(false);
  canManage = signal(false);

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<string | null>(null);
  loadApiError = signal<ApiError | null>(null);
  devices = signal<WallDeviceView[]>([]);

  /** Current time in ms. Refreshed with the list and each second while a pairing key is showing. */
  now = signal(Date.now());

  notice = signal<string | null>(null);
  actionError = signal<string | null>(null);
  confirmingRevokeId = signal<string | null>(null);
  revokingId = signal<string | null>(null);

  // Configure dialog
  editing = signal<WallDeviceView | null>(null);
  editName = signal('');
  editExitCode = signal('');
  editSaving = signal(false);
  editError = signal<string | null>(null);

  // Add dialog
  modalOpen = signal(false);
  creating = signal(false);
  modalError = signal<string | null>(null);
  name = signal('');
  newExitCode = signal('');
  created = signal<WallDeviceCreated | null>(null);
  /** Seconds left on the pairing key. Zero once it has expired. */
  secondsLeft = computed(() => {
    const key = this.created();
    if (!key) return 0;
    return Math.max(0, Math.ceil((Date.parse(key.expires_at) - this.now()) / 1000));
  });
  countdownLabel = computed(() => {
    const s = this.secondsLeft();
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
  });
  private countdown?: Subscription;

  constructor() {
    this.canManage.set(this.auth.isParent());
    this.permsReady.set(true);
    if (this.canManage()) this.load();

    interval(REFRESH_MS)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        if (this.loadState() === 'ready' && !this.revokingId()) this.refresh();
      });
  }

  load(): void {
    if (!this.canManage()) return;
    this.loadState.set('loading');
    this.loadError.set(null);
    this.loadApiError.set(null);
    this.devicesApi
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (devices) => {
          this.setDevices(devices);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(settingsErrorKey(err));
          this.loadApiError.set(err);
          this.loadState.set('error');
        },
      });
  }

  /** Quiet reload for status and last seen. Keeps the page as it is if it fails. */
  private refresh(): void {
    this.devicesApi
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({ next: (devices) => this.setDevices(devices), error: () => undefined });
  }

  /** Closes the Configure dialog if its device has gone, for example after a revoke elsewhere. */
  private setDevices(devices: WallDeviceView[]): void {
    this.devices.set(devices);
    this.now.set(Date.now());
    const open = this.editing();
    if (open && !devices.some((d) => d.id === open.id)) this.editing.set(null);
  }

  private replaceDevice(updated: WallDeviceView): void {
    this.devices.update((list) => list.map((d) => (d.id === updated.id ? updated : d)));
  }

  // Configure

  openEdit(device: WallDeviceView): void {
    this.editing.set(device);
    this.editName.set(device.name);
    this.editExitCode.set('');
    this.editError.set(null);
    this.notice.set(null);
    this.actionError.set(null);
  }

  closeEdit(): void {
    if (this.editSaving()) return;
    this.editing.set(null);
  }

  closeEditOnBackdrop(e: Event): void {
    if (e.target === e.currentTarget) this.closeEdit();
  }

  /** Sends only what changed. A blank exit code keeps the current one. */
  saveEdit(): void {
    const device = this.editing();
    if (!device || this.editSaving()) return;
    const name = this.editName().trim();
    const exitCode = this.editExitCode().trim();
    if (name === '') {
      this.editError.set('settings.error.required');
      return;
    }
    if ([...name].length > MAX_NAME_LENGTH) {
      this.editError.set('settings.error.too_long');
      return;
    }
    if (exitCode !== '' && !EXIT_CODE.test(exitCode)) {
      this.editError.set('settings.wall.error.exit_code');
      return;
    }

    const body: WallDeviceUpdate = {};
    if (name !== device.name) body.name = name;
    if (exitCode !== '') body.exit_code = exitCode;
    if (Object.keys(body).length === 0) {
      this.editing.set(null);
      return;
    }

    this.editSaving.set(true);
    this.editError.set(null);
    this.devicesApi
      .update(device.id, body)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.editSaving.set(false)),
      )
      .subscribe({
        next: (updated) => {
          this.replaceDevice(updated);
          this.editing.set(null);
          this.notice.set(this.transloco.translate('settings.wall.saved'));
        },
        error: (err: ApiError) => this.editError.set(settingsErrorKey(err)),
      });
  }

  // Revoke

  askRevoke(device: WallDeviceView): void {
    this.actionError.set(null);
    this.notice.set(null);
    this.confirmingRevokeId.set(device.id);
  }

  cancelRevoke(): void {
    this.confirmingRevokeId.set(null);
  }

  revoke(device: WallDeviceView): void {
    if (this.revokingId()) return;
    this.revokingId.set(device.id);
    this.actionError.set(null);
    this.devicesApi
      .revoke(device.id)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.revokingId.set(null)),
      )
      .subscribe({
        next: () => {
          this.confirmingRevokeId.set(null);
          this.devices.update((list) => list.filter((d) => d.id !== device.id));
          if (this.editing()?.id === device.id) this.editing.set(null);
          this.notice.set(this.transloco.translate('settings.wall.revoked'));
        },
        error: (err: ApiError) => this.actionError.set(settingsErrorKey(err)),
      });
  }

  /** Relative time since the device last reported in. Pending devices have never reported. */
  lastSeenLabel(device: WallDeviceView): string {
    if (!device.last_seen_at) return this.transloco.translate('settings.wall.last_seen.never');
    const seconds = Math.max(0, Math.floor((this.now() - Date.parse(device.last_seen_at)) / 1000));
    if (seconds < 10) return this.transloco.translate('settings.wall.last_seen.now');
    if (seconds < 60) return this.transloco.translate('settings.wall.last_seen.seconds', { count: seconds });
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return this.transloco.translate('settings.wall.last_seen.minutes', { count: minutes });
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return this.transloco.translate('settings.wall.last_seen.hours', { count: hours });
    return this.transloco.translate('settings.wall.last_seen.days', { count: Math.floor(hours / 24) });
  }

  // Add dialog

  openNew(): void {
    this.name.set('');
    this.newExitCode.set('');
    this.created.set(null);
    this.creating.set(false);
    this.modalError.set(null);
    this.notice.set(null);
    this.modalOpen.set(true);
  }

  closeModal(): void {
    if (this.creating()) return;
    const wasCreated = this.created() !== null;
    this.stopCountdown();
    this.modalOpen.set(false);
    this.created.set(null);
    if (wasCreated) this.refresh();
  }

  closeOnBackdrop(e: Event): void {
    if (e.target === e.currentTarget) this.closeModal();
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.closeModal();
    this.closeEdit();
    this.cancelRevoke();
  }

  /** The first problem with the dialog values, as a translation key. Null when valid. */
  validationKey(): string | null {
    const name = this.name().trim();
    if (name === '') return 'settings.error.required';
    if ([...name].length > MAX_NAME_LENGTH) return 'settings.error.too_long';
    if (!EXIT_CODE.test(this.newExitCode().trim())) return 'settings.wall.error.exit_code';
    return null;
  }

  create(): void {
    if (this.creating() || this.created()) return;
    const key = this.validationKey();
    if (key) {
      this.modalError.set(key);
      return;
    }
    this.creating.set(true);
    this.modalError.set(null);
    this.devicesApi
      .create({ name: this.name().trim(), exit_code: this.newExitCode().trim() })
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.creating.set(false)),
      )
      .subscribe({
        next: (res) => {
          this.created.set(res);
          this.startCountdown();
        },
        error: (err: ApiError) => this.modalError.set(settingsErrorKey(err)),
      });
  }

  private startCountdown(): void {
    this.stopCountdown();
    this.now.set(Date.now());
    this.countdown = interval(1000).subscribe(() => this.now.set(Date.now()));
  }

  private stopCountdown(): void {
    this.countdown?.unsubscribe();
    this.countdown = undefined;
  }

  ngOnDestroy(): void {
    this.stopCountdown();
  }
}
