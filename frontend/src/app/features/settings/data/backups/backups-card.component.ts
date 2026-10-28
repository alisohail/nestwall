import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, computed, inject, signal, DOCUMENT } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { catchError, filter, finalize, of, switchMap, take, timer } from 'rxjs';
import { ApiError } from '../../../../core/api/api-client.service';
import { AuthService } from '../../../../core/auth/auth.service';
import { TimeFormatService } from '../../../../core/time-format/time-format.service';
import { BannerComponent } from '../../../../shared/ui/banner/banner.component';
import { IconComponent } from '../../../../shared/ui/icon/icon.component';
import { LoadingComponent } from '../../../../shared/ui/loading/loading.component';
import { SkeletonComponent } from '../../../../shared/ui/skeleton/skeleton.component';
import { BackupRun, BackupStatus, BackupsService, ResetCounts } from './backups.service';

/** Matches MinPassphraseLength on the server. */
const PASSPHRASE_MIN = 8;

/** The user must type this word before a restore can run. */
const RESTORE_WORD = 'RESTORE';

/** The user must type this word before a factory reset can run. Matches ResetWord on the server. */
const RESET_WORD = 'DELETE EVERYTHING';

/** Matches the server bounds in SaveSchedule (migration 000055). */
const SCHEDULE_LIMITS = {
  hour: [0, 23],
  keep: [1, 60],
} as const;

/** Error keys shown in the card. Each one is under settings.data.backups.error. */
function errorKey(err: ApiError, fallback: string): string {
  switch (err.code) {
    case 'password_incorrect':
    case 'passphrase_required':
    case 'passphrase_too_short':
    case 'invalid_schedule':
    case 'backup_missing':
    case 'backup_not_found':
    case 'backup_newer_version':
    case 'restore_unreadable':
    case 'restore_in_progress':
      return `settings.data.backups.error.${err.code}`;
  }
  return `settings.data.backups.error.${fallback}`;
}

/** Error keys for the factory reset dialog. Each one is under settings.data.reset.error. */
function resetErrorKey(err: ApiError): string {
  switch (err.code) {
    case 'password_incorrect':
    case 'confirm_incorrect':
    case 'reset_backup_failed':
    case 'restore_in_progress':
      return `settings.data.reset.error.${err.code}`;
  }
  return 'settings.data.reset.error.reset_failed';
}

/** The Backups card on Settings > Data and backups: status, schedule, encryption, run now and the history. */
@Component({
    selector: 'app-backups-card',
    imports: [FormsModule, DatePipe, TranslocoModule, BannerComponent, IconComponent, LoadingComponent, SkeletonComponent],
    templateUrl: './backups-card.component.html',
    styleUrl: './backups-card.component.scss'
})
export class BackupsCardComponent implements OnInit {
  private api = inject(BackupsService);
  private auth = inject(AuthService);
  timeFormat = inject(TimeFormatService);
  private destroyRef = inject(DestroyRef);
  private document = inject(DOCUMENT);

  /** Restore needs `backup.restore` and reset needs `data.reset`. The server checks both too. */
  readonly canRestore = this.auth.can('backup.restore');
  readonly canReset = this.auth.can('data.reset');

  readonly passphraseMin = PASSPHRASE_MIN;

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  status = signal<BackupStatus | null>(null);

  running = signal(false);
  runResult = signal<'ok' | 'failed' | null>(null);

  passphraseOpen = signal(false);
  encryptBusy = signal(false);
  encryptError = signal<string | null>(null);
  passphrase = '';

  scheduleOpen = signal(false);
  scheduleBusy = signal(false);
  scheduleError = signal<string | null>(null);
  draftHour = 2;
  draftKeep = 6;
  readonly hours = Array.from({ length: 24 }, (_, h) => h);
  readonly limits = SCHEDULE_LIMITS;

  downloadingId = signal<string | null>(null);
  downloadBusy = signal(false);
  downloadError = signal<string | null>(null);
  downloadPassword = '';

  readonly restoreWord = RESTORE_WORD;
  restoreTarget = signal<BackupRun | null>(null);
  /** A zip the parent chose from disk. It is used instead of a kept archive. */
  restoreFile = signal<File | null>(null);
  restoreOpen = computed(() => this.restoreTarget() !== null || this.restoreFile() !== null);
  restoreBusy = signal(false);
  restoreError = signal<string | null>(null);
  /** Set once the server accepts a restore. The API then restarts, and the page reloads when it answers again. */
  restarting = signal(false);
  /** The full-page overlay is shown while the restore runs, while the API restarts, and during a factory reset. */
  overlayVisible = computed(() => this.restoreBusy() || this.restarting() || this.resetting());
  restorePassword = '';
  restoreTyped = '';

  readonly resetWord = RESET_WORD;
  /** Replaced in tests, so a successful reset does not reload the test page. */
  reloadPage = (): void => this.document.location.reload();
  resetOpen = signal(false);
  /** Null until the preview loads. The dialog shows the exact counts before anything is removed. */
  resetCounts = signal<ResetCounts | null>(null);
  resetCountsError = signal(false);
  resetError = signal<string | null>(null);
  /** Set while the reset request runs. It stays set after success, so the overlay covers the page until the reload. */
  resetting = signal(false);
  resetPassword = '';
  resetTyped = '';

  /** Kept archives are the ones that finished and were verified. */
  keptCount = computed(() => (this.status()?.history ?? []).filter((r) => r.status === 'ok').length);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loadState.set('loading');
    this.api
      .get()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (s) => {
          this.status.set(s);
          this.loadState.set('ready');
        },
        error: () => this.loadState.set('error'),
      });
  }

  /** Turning encryption on needs a passphrase, so the form opens first. Turning it off keeps the stored passphrase. */
  onEncryptToggle(event: Event): void {
    const input = event.target as HTMLInputElement;
    const wantOn = input.checked;
    const s = this.status();
    if (!s) return;
    this.encryptError.set(null);
    if (wantOn && !s.passphrase_set) {
      input.checked = false;
      this.openPassphrase();
      return;
    }
    this.saveEncryption(wantOn, null);
  }

  openPassphrase(): void {
    this.passphrase = '';
    this.encryptError.set(null);
    this.passphraseOpen.set(true);
  }

  closePassphrase(): void {
    this.passphrase = '';
    this.passphraseOpen.set(false);
  }

  savePassphrase(): void {
    if (this.encryptBusy()) return;
    if (this.passphrase.length < PASSPHRASE_MIN) {
      this.encryptError.set('settings.data.backups.error.passphrase_too_short');
      return;
    }
    this.saveEncryption(true, this.passphrase);
  }

  openSchedule(): void {
    const s = this.status();
    if (!s) return;
    this.draftHour = s.schedule_hour;
    this.draftKeep = s.keep_count;
    this.scheduleError.set(null);
    this.scheduleOpen.set(true);
  }

  closeSchedule(): void {
    this.scheduleError.set(null);
    this.scheduleOpen.set(false);
  }

  saveSchedule(): void {
    if (!this.scheduleValid()) {
      this.scheduleError.set('settings.data.backups.error.invalid_schedule');
      return;
    }
    this.persistSchedule(this.draftHour, this.draftKeep);
  }

  /** The hour select saves at once and keeps the stored keep count. */
  changeHour(hour: number): void {
    const s = this.status();
    if (!s || !Number.isInteger(hour) || hour === s.schedule_hour) return;
    this.persistSchedule(hour, s.keep_count);
  }

  private persistSchedule(hour: number, keepCount: number): void {
    if (this.scheduleBusy()) return;
    this.scheduleBusy.set(true);
    this.scheduleError.set(null);
    this.api
      .setSchedule(hour, keepCount)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.scheduleBusy.set(false)),
      )
      .subscribe({
        next: (s) => {
          this.status.set(s);
          this.closeSchedule();
        },
        error: (err: ApiError) => this.scheduleError.set(errorKey(err, 'save_failed')),
      });
  }

  /** Empty or out-of-range inputs are rejected here as well as by the server. */
  private scheduleValid(): boolean {
    const inRange = (v: number, [min, max]: readonly [number, number]) => Number.isInteger(v) && v >= min && v <= max;
    return inRange(this.draftHour, SCHEDULE_LIMITS.hour) && inRange(this.draftKeep, SCHEDULE_LIMITS.keep);
  }

  runNow(): void {
    if (this.running()) return;
    this.running.set(true);
    this.runResult.set(null);
    this.api
      .runNow()
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.running.set(false)),
      )
      .subscribe({
        next: (s) => {
          this.status.set(s);
          this.runResult.set('ok');
        },
        error: () => this.runResult.set('failed'),
      });
  }

  startDownload(run: BackupRun): void {
    this.downloadingId.set(run.id);
    this.downloadPassword = '';
    this.downloadError.set(null);
  }

  cancelDownload(): void {
    this.downloadingId.set(null);
    this.downloadPassword = '';
    this.downloadError.set(null);
  }

  confirmDownload(run: BackupRun): void {
    if (this.downloadBusy() || this.downloadPassword === '') return;
    this.downloadBusy.set(true);
    this.downloadError.set(null);
    this.api
      .download(run.id, this.downloadPassword)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.downloadBusy.set(false)),
      )
      .subscribe({
        next: (blob) => {
          this.save(this.fileName(run), blob);
          this.cancelDownload();
        },
        error: (err: ApiError) => {
          // The password field is cleared on failure, so the prompt stays open for a retry.
          this.downloadPassword = '';
          this.downloadError.set(errorKey(err, 'download_failed'));
        },
      });
  }

  openRestore(run: BackupRun): void {
    this.restoreTarget.set(run);
    this.restoreFile.set(null);
    this.resetRestoreFields();
  }

  /** Reads the zip the parent picked and opens the same confirm dialog as a kept archive. */
  chooseRestoreFile(input: HTMLInputElement): void {
    const file = input.files?.[0] ?? null;
    input.value = '';
    if (!file) return;
    this.restoreTarget.set(null);
    this.restoreFile.set(file);
    this.resetRestoreFields();
  }

  closeRestore(): void {
    if (this.restoreBusy()) return;
    this.restoreTarget.set(null);
    this.restoreFile.set(null);
    this.resetRestoreFields();
  }

  private resetRestoreFields(): void {
    this.restorePassword = '';
    this.restoreTyped = '';
    this.restoreError.set(null);
  }

  /** The restore needs the account password and the typed word. The server replaces the data and restarts the API. */
  confirmRestore(): void {
    const run = this.restoreTarget();
    const file = this.restoreFile();
    if ((!run && !file) || this.restoreBusy() || this.restoreTyped !== RESTORE_WORD || this.restorePassword === '') return;
    this.restoreBusy.set(true);
    this.restoreError.set(null);
    const request = file
      ? this.api.restoreUpload(file, this.restorePassword)
      : this.api.restore(run!.id, this.restorePassword);
    request
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.restoreBusy.set(false)),
      )
      .subscribe({
        next: () => {
          this.restorePassword = '';
          this.restoreTarget.set(null);
          this.restoreFile.set(null);
          this.restoreTyped = '';
          this.restarting.set(true);
          this.reloadWhenBack();
        },
        error: (err: ApiError) => {
          // The password is cleared on failure, so the dialog stays open for a retry.
          this.restorePassword = '';
          this.restoreError.set(errorKey(err, 'restore_failed'));
        },
      });
  }

  /** Polls the liveness probe until the restarted API answers, then reloads so the app shows the restored data. */
  private reloadWhenBack(): void {
    timer(1500, 2000)
      .pipe(
        switchMap(() => this.api.live().pipe(catchError(() => of(null)))),
        filter((res) => res !== null),
        take(1),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(() => this.document.location.reload());
  }

  /** Which line the full-page overlay shows. */
  overlayKey(): string {
    if (this.restarting()) return 'settings.data.backups.restarting';
    if (this.resetting()) return 'settings.data.reset.working';
    return 'settings.data.backups.restoring';
  }

  openReset(): void {
    this.resetOpen.set(true);
    this.resetCounts.set(null);
    this.resetCountsError.set(false);
    this.resetFields();
    this.api
      .resetPreview()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (counts) => this.resetCounts.set(counts),
        error: () => this.resetCountsError.set(true),
      });
  }

  closeReset(): void {
    if (this.resetting()) return;
    this.resetOpen.set(false);
    this.resetFields();
  }

  private resetFields(): void {
    this.resetPassword = '';
    this.resetTyped = '';
    this.resetError.set(null);
  }

  /** The reset needs the typed word and the account password. The server backs up first, then removes the data. */
  confirmReset(): void {
    if (this.resetting() || this.resetTyped !== RESET_WORD || this.resetPassword === '') return;
    this.resetting.set(true);
    this.resetError.set(null);
    this.api
      .factoryReset(this.resetPassword, this.resetTyped)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => this.reloadPage(),
        error: (err: ApiError) => {
          // The password is cleared on failure, so the dialog stays open for a retry.
          this.resetPassword = '';
          this.resetting.set(false);
          this.resetError.set(resetErrorKey(err));
        },
      });
  }

  /** The file name the browser suggests. It is built from the run time, so the saved copies sort by date. */
  fileName(run: BackupRun): string {
    const stamp = new Date(run.started_at).toISOString().slice(0, 16).replace(/[-:T]/g, '');
    return `nestwall-backup-${stamp}.zip`;
  }

  /** A Date for the schedule hour, so the date pipe formats it in the user's locale. */
  scheduleTime(hour: number): Date {
    return new Date(2000, 0, 1, hour, 0);
  }

  sizeLabel(bytes: number): string {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  kindKey(kind: string): string {
    switch (kind) {
      case 'manual':
        return 'settings.data.backups.kind_manual';
      case 'before_reset':
        return 'settings.data.backups.kind_before_reset';
      default:
        return 'settings.data.backups.kind_scheduled';
    }
  }

  private saveEncryption(encrypt: boolean, passphrase: string | null): void {
    this.encryptBusy.set(true);
    this.encryptError.set(null);
    this.api
      .setEncryption(encrypt, passphrase)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.encryptBusy.set(false)),
      )
      .subscribe({
        next: (s) => {
          this.status.set(s);
          this.closePassphrase();
        },
        error: (err: ApiError) => this.encryptError.set(errorKey(err, 'save_failed')),
      });
  }

  private save(name: string, file: Blob): void {
    const url = URL.createObjectURL(file);
    const link = this.document.createElement('a');
    link.href = url;
    link.download = name;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url));
  }
}
