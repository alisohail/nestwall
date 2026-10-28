import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Subject, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../../core/transloco/transloco-testing.module';
import { AuthService } from '../../../../core/auth/auth.service';
import { BackupRun, BackupStatus, BackupsService, ResetCounts } from './backups.service';
import { BackupsCardComponent } from './backups-card.component';

const GOOD: BackupRun = {
  id: 'run-good',
  kind: 'scheduled',
  status: 'ok',
  verified: true,
  encrypted: true,
  size_bytes: 4404019,
  started_at: '2026-01-10T02:00:00Z',
  downloadable: true,
};

const FAILED: BackupRun = {
  id: 'run-failed',
  kind: 'manual',
  status: 'failed',
  verified: false,
  encrypted: false,
  size_bytes: 0,
  error_message: 'disk full',
  started_at: '2026-01-09T09:30:00Z',
  downloadable: false,
};

const STATUS: BackupStatus = {
  last_good: GOOD,
  next_run_at: '2026-01-11T02:00:00Z',
  schedule_hour: 2,
  keep_count: 6,
  encrypt: true,
  passphrase_set: true,
  location: '/app/database/backups',
  history: [GOOD, FAILED],
};

describe('BackupsCardComponent', () => {
  let fixture: ComponentFixture<BackupsCardComponent>;
  let api: jasmine.SpyObj<BackupsService>;
  let grants: string[];

  function setup(status: BackupStatus = STATUS): void {
    api = jasmine.createSpyObj<BackupsService>('BackupsService', ['get', 'setEncryption', 'setSchedule', 'runNow', 'download', 'restore', 'restoreUpload', 'resetPreview', 'factoryReset', 'live']);
    api.get.and.returnValue(of(status));
    api.setEncryption.and.callFake((encrypt: boolean) => of({ ...status, encrypt, passphrase_set: true }));
    api.setSchedule.and.callFake((hour: number, keepCount: number) => of({ ...status, schedule_hour: hour, keep_count: keepCount }));
    api.runNow.and.returnValue(of(status));
    api.download.and.returnValue(of(new Blob(['x'])));
    api.restore.and.returnValue(of({ safety_backup: 'nestwall-safety.zip', restarting: true }));
    api.restoreUpload.and.returnValue(of({ safety_backup: 'nestwall-safety.zip', restarting: true }));
    api.live.and.returnValue(of({ status: 'ok' }));

    TestBed.configureTestingModule({
      imports: [BackupsCardComponent, getTranslocoTestingModule()],
      providers: [
        { provide: BackupsService, useValue: api },
        { provide: AuthService, useValue: { can: (perm: string) => grants.includes(perm), currentUser: () => null } },
      ],
    });
    fixture = TestBed.createComponent(BackupsCardComponent);
    fixture.detectChanges();
  }

  beforeEach(() => {
    grants = ['backup.manage', 'backup.restore', 'data.reset'];
  });

  it('hides restore and upload without backup.restore, and keeps the list', () => {
    grants = ['backup.manage'];
    setup();

    expect(el('[data-testid="backups-upload"]')).toBeNull();
    expect(el('[data-testid="backup-restore-run-good"]')).toBeNull();
    expect(el('[data-testid="backup-row-run-good"]')).not.toBeNull();
  });

  it('hides the factory reset card without data.reset', () => {
    grants = ['backup.manage', 'backup.restore'];
    setup();

    expect(el('[data-testid="reset-card"]')).toBeNull();
  });

  it('shows restore, upload and reset when the codes are granted', () => {
    setup();

    expect(el('[data-testid="backups-upload"]')).not.toBeNull();
    expect(el('[data-testid="backup-restore-run-good"]')).not.toBeNull();
    expect(el('[data-testid="reset-card"]')).not.toBeNull();
  });

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

  function toggleEncrypt(checked: boolean): void {
    const input = el<HTMLInputElement>('[data-testid="backups-encrypt"]');
    if (!input) throw new Error('missing encrypt switch');
    input.checked = checked;
    input.dispatchEvent(new Event('change'));
    fixture.detectChanges();
  }

  it('shows the verified health and counts only the runs that passed', async () => {
    setup();
    await fixture.whenStable();
    fixture.detectChanges();
    expect(el('[data-testid="backups-health"]')?.textContent).toContain('Last backup OK');
    expect(el('[data-testid="backups-kept"]')?.textContent).toContain('1 backups');
    expect(el<HTMLSelectElement>('[data-testid="backups-schedule-hour"]')?.selectedOptions[0]?.textContent).toContain('2:00 AM');
  });

  it('saves a new schedule hour and keep count', () => {
    setup();
    const c = fixture.componentInstance;
    c.openSchedule();
    fixture.detectChanges();
    c.draftHour = 5;
    c.draftKeep = 14;
    c.saveSchedule();
    expect(api.setSchedule).toHaveBeenCalledWith(5, 14);
  });

  it('saves a new hour at once and keeps the stored keep count', () => {
    setup();
    fixture.componentInstance.changeHour(5);
    expect(api.setSchedule).toHaveBeenCalledWith(5, STATUS.keep_count);
  });

  it('blocks an out-of-range keep count without calling the server', () => {
    setup();
    const c = fixture.componentInstance;
    c.openSchedule();
    c.draftKeep = 61;
    c.saveSchedule();
    expect(api.setSchedule).not.toHaveBeenCalled();
    expect(c.scheduleError()).toBe('settings.data.backups.error.invalid_schedule');
  });

  it('lists the runs and disables download for the failed one', () => {
    setup();
    expect(el<HTMLButtonElement>(`[data-testid="backup-row-${GOOD.id}"] button`)?.disabled).toBeFalse();
    expect(el<HTMLButtonElement>(`[data-testid="backup-row-${FAILED.id}"] button`)?.disabled).toBeTrue();
  });

  it('opens the passphrase form instead of turning encryption on without a stored passphrase', () => {
    setup({ ...STATUS, encrypt: false, passphrase_set: false });
    toggleEncrypt(true);
    expect(api.setEncryption).not.toHaveBeenCalled();
    expect(el('[data-testid="backups-passphrase-form"]')).not.toBeNull();
    expect(el<HTMLInputElement>('[data-testid="backups-encrypt"]')?.checked).toBeFalse();
  });

  it('saves the new passphrase with encryption on', async () => {
    setup({ ...STATUS, encrypt: false, passphrase_set: false });
    toggleEncrypt(true);
    await fixture.whenStable();
    type('[data-testid="backups-passphrase"]', 'long enough phrase');
    fixture.componentInstance.savePassphrase();
    fixture.detectChanges();
    expect(api.setEncryption).toHaveBeenCalledWith(true, 'long enough phrase');
  });

  it('rejects a passphrase shorter than 8 characters without calling the server', async () => {
    setup({ ...STATUS, encrypt: false, passphrase_set: false });
    toggleEncrypt(true);
    await fixture.whenStable();
    type('[data-testid="backups-passphrase"]', 'short');
    fixture.componentInstance.savePassphrase();
    fixture.detectChanges();
    expect(api.setEncryption).not.toHaveBeenCalled();
    expect(fixture.componentInstance.encryptError()).toBe('settings.data.backups.error.passphrase_too_short');
  });

  it('turns encryption off and keeps the stored passphrase', () => {
    setup();
    toggleEncrypt(false);
    expect(api.setEncryption).toHaveBeenCalledWith(false, null);
  });

  it('shows the password error and keeps the prompt open when the password is wrong', async () => {
    setup();
    api.download.and.returnValue(throwError(() => ({ code: 'password_incorrect', status: 403 })));
    el<HTMLButtonElement>(`[data-testid="backup-row-${GOOD.id}"] button`)?.click();
    fixture.detectChanges();
    await fixture.whenStable();
    type('[data-testid="backups-download-password"]', 'wrong');
    fixture.componentInstance.confirmDownload(GOOD);
    fixture.detectChanges();
    expect(api.download).toHaveBeenCalledWith(GOOD.id, 'wrong');
    expect(el('[data-testid="backups-download-form"]')).not.toBeNull();
    expect(el('[data-testid="backups-history"]')?.textContent).toContain('not right');
  });

  it('shows success after a manual run', () => {
    setup();
    el<HTMLButtonElement>('[data-testid="backups-run"]')?.click();
    fixture.detectChanges();
    expect(api.runNow).toHaveBeenCalled();
    expect(el('[data-testid="backups-run-ok"]')).not.toBeNull();
  });

  it('shows the failure banner when a manual run fails', () => {
    setup();
    api.runNow.and.returnValue(throwError(() => ({ code: 'backup_failed', status: 500 })));
    el<HTMLButtonElement>('[data-testid="backups-run"]')?.click();
    fixture.detectChanges();
    expect(el('[data-testid="backups-run-failed"]')).not.toBeNull();
  });

  it('opens the restore dialog for a downloadable run and keeps restore disabled until the password and word are in', () => {
    setup();
    el<HTMLButtonElement>(`[data-testid="backup-restore-${GOOD.id}"]`)?.click();
    fixture.detectChanges();
    expect(el('[data-testid="backups-restore-dialog"]')).not.toBeNull();
    expect(el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.disabled).toBeTrue();
    type('[data-testid="backups-restore-password"]', 'pw');
    expect(el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.disabled).toBeTrue();
    type('[data-testid="backups-restore-typed"]', 'restore');
    expect(el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.disabled).toBeTrue();
    type('[data-testid="backups-restore-typed"]', 'RESTORE');
    expect(el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.disabled).toBeFalse();
  });

  it('disables restore for a run whose file is gone', () => {
    setup();
    expect(el<HTMLButtonElement>(`[data-testid="backup-restore-${FAILED.id}"]`)?.disabled).toBeTrue();
  });

  it('restores with the password, then shows the restarting overlay', () => {
    setup();
    el<HTMLButtonElement>(`[data-testid="backup-restore-${GOOD.id}"]`)?.click();
    fixture.detectChanges();
    type('[data-testid="backups-restore-password"]', 'correct horse');
    type('[data-testid="backups-restore-typed"]', 'RESTORE');
    el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.click();
    fixture.detectChanges();
    expect(api.restore).toHaveBeenCalledWith(GOOD.id, 'correct horse');
    expect(el('[data-testid="backups-restore-dialog"]')).toBeNull();
    expect(el('[data-testid="backups-restore-overlay"]')?.textContent).toContain('Restarting');
  });

  it('shows the loading overlay while the restore request is still running', () => {
    setup();
    const pending = new Subject<{ safety_backup: string; restarting: boolean }>();
    api.restore.and.returnValue(pending.asObservable());
    el<HTMLButtonElement>(`[data-testid="backup-restore-${GOOD.id}"]`)?.click();
    fixture.detectChanges();
    type('[data-testid="backups-restore-password"]', 'pw');
    type('[data-testid="backups-restore-typed"]', 'RESTORE');
    el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.click();
    fixture.detectChanges();
    expect(el('[data-testid="backups-restore-overlay"]')?.textContent).toContain('Restoring your backup');
    expect(el('[data-testid="backups-restore-dialog"]')).toBeNull();
    expect(el<HTMLButtonElement>('[data-testid="backups-upload"]')?.disabled).toBeTrue();
  });

  it('restores from a chosen zip with its name in the dialog and the password', () => {
    setup();
    const file = new File(['zip'], 'my-copy.zip', { type: 'application/zip' });
    fixture.componentInstance.chooseRestoreFile({ files: [file], value: 'x' } as unknown as HTMLInputElement);
    fixture.detectChanges();
    expect(el('[data-testid="backups-restore-message"]')?.textContent).toContain('my-copy.zip');
    type('[data-testid="backups-restore-password"]', 'correct horse');
    type('[data-testid="backups-restore-typed"]', 'RESTORE');
    el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.click();
    fixture.detectChanges();
    expect(api.restoreUpload).toHaveBeenCalledWith(file, 'correct horse');
    expect(api.restore).not.toHaveBeenCalled();
    expect(el('[data-testid="backups-restore-overlay"]')).not.toBeNull();
  });

  it('does nothing when the file picker is closed without a file', () => {
    setup();
    fixture.componentInstance.chooseRestoreFile({ files: null, value: 'x' } as unknown as HTMLInputElement);
    fixture.detectChanges();
    expect(el('[data-testid="backups-restore-dialog"]')).toBeNull();
  });

  it('keeps the dialog open with the password error when the password is wrong', () => {
    setup();
    api.restore.and.returnValue(throwError(() => ({ code: 'password_incorrect', status: 403 })));
    el<HTMLButtonElement>(`[data-testid="backup-restore-${GOOD.id}"]`)?.click();
    fixture.detectChanges();
    type('[data-testid="backups-restore-password"]', 'wrong');
    type('[data-testid="backups-restore-typed"]', 'RESTORE');
    el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.click();
    fixture.detectChanges();
    expect(el('[data-testid="backups-restore-dialog"]')).not.toBeNull();
    expect(el('[data-testid="backups-restore-error"]')?.textContent).toContain('not right');
    expect(fixture.componentInstance.restorePassword).toBe('');
    expect(el('[data-testid="backups-restore-overlay"]')).toBeNull();
  });

  it('maps an unreadable archive to its own message', () => {
    setup();
    api.restore.and.returnValue(throwError(() => ({ code: 'restore_unreadable', status: 422 })));
    el<HTMLButtonElement>(`[data-testid="backup-restore-${GOOD.id}"]`)?.click();
    fixture.detectChanges();
    type('[data-testid="backups-restore-password"]', 'pw');
    type('[data-testid="backups-restore-typed"]', 'RESTORE');
    el<HTMLButtonElement>('[data-testid="backups-restore-ok"]')?.click();
    fixture.detectChanges();
    expect(el('[data-testid="backups-restore-error"]')?.textContent).toContain('could not be opened');
  });

  describe('factory reset', () => {
    const COUNTS: ResetCounts = {
      members: 6,
      children: 3,
      tasks: 214,
      points_entries: 1032,
      events: 12,
      avatars: 6,
      wall_devices: 2,
      api_tokens: 4,
    };

    function setupReset(): void {
      setup();
      api.resetPreview = jasmine.createSpy('resetPreview').and.returnValue(of(COUNTS));
      api.factoryReset = jasmine.createSpy('factoryReset').and.returnValue(of({ reset: true }));
      fixture.componentInstance.reloadPage = jasmine.createSpy('reloadPage');
    }

    function openDialog(): void {
      el<HTMLButtonElement>('[data-testid="reset-open"]')?.click();
      fixture.detectChanges();
    }

    it('shows the exact counts from the preview before anything is removed', () => {
      setupReset();
      openDialog();
      expect(api.resetPreview).toHaveBeenCalled();
      expect(el('[data-testid="reset-dialog"]')).not.toBeNull();
      const counts = el('[data-testid="reset-counts"]')?.textContent ?? '';
      expect(counts).toContain('6 members');
      expect(counts).toContain('214 tasks');
      expect(counts).toContain('1032 points entries');
      expect(api.factoryReset).not.toHaveBeenCalled();
    });

    it('keeps the confirm button disabled until the word and the password are in', () => {
      setupReset();
      openDialog();
      const confirm = () => el<HTMLButtonElement>('[data-testid="reset-confirm"]');
      expect(confirm()?.disabled).toBeTrue();
      type('[data-testid="reset-typed"]', 'DELETE EVERYTHING');
      expect(confirm()?.disabled).toBeTrue();
      type('[data-testid="reset-password"]', 'pw');
      expect(confirm()?.disabled).toBeFalse();
    });

    it('resets with the typed word and the password, then reloads the page', () => {
      setupReset();
      openDialog();
      type('[data-testid="reset-typed"]', 'DELETE EVERYTHING');
      type('[data-testid="reset-password"]', 'pw');
      el<HTMLButtonElement>('[data-testid="reset-confirm"]')?.click();
      fixture.detectChanges();
      expect(api.factoryReset).toHaveBeenCalledWith('pw', 'DELETE EVERYTHING');
      expect(fixture.componentInstance.reloadPage).toHaveBeenCalled();
    });

    it('shows the resetting overlay while the request runs', () => {
      setupReset();
      const pending = new Subject<unknown>();
      api.factoryReset.and.returnValue(pending.asObservable());
      openDialog();
      type('[data-testid="reset-typed"]', 'DELETE EVERYTHING');
      type('[data-testid="reset-password"]', 'pw');
      el<HTMLButtonElement>('[data-testid="reset-confirm"]')?.click();
      fixture.detectChanges();
      expect(el('[data-testid="reset-dialog"]')).toBeNull();
      expect(el('[data-testid="backups-restore-overlay"]')?.textContent).toContain('Resetting NestWall');
    });

    it('keeps the dialog open with the password error when the password is wrong', () => {
      setupReset();
      api.factoryReset.and.returnValue(throwError(() => ({ code: 'password_incorrect', status: 403 })));
      openDialog();
      type('[data-testid="reset-typed"]', 'DELETE EVERYTHING');
      type('[data-testid="reset-password"]', 'wrong');
      el<HTMLButtonElement>('[data-testid="reset-confirm"]')?.click();
      fixture.detectChanges();
      expect(el('[data-testid="reset-dialog"]')).not.toBeNull();
      expect(el('[data-testid="reset-error"]')?.textContent).toContain('not right');
      expect(fixture.componentInstance.resetPassword).toBe('');
      expect(fixture.componentInstance.reloadPage).not.toHaveBeenCalled();
    });

    it('explains that nothing was removed when the backup before the reset fails', () => {
      setupReset();
      api.factoryReset.and.returnValue(throwError(() => ({ code: 'reset_backup_failed', status: 500 })));
      openDialog();
      type('[data-testid="reset-typed"]', 'DELETE EVERYTHING');
      type('[data-testid="reset-password"]', 'pw');
      el<HTMLButtonElement>('[data-testid="reset-confirm"]')?.click();
      fixture.detectChanges();
      expect(el('[data-testid="reset-error"]')?.textContent).toContain('nothing was removed');
    });

    it('does not call the server when the typed word is wrong', () => {
      setupReset();
      openDialog();
      type('[data-testid="reset-typed"]', 'delete everything');
      type('[data-testid="reset-password"]', 'pw');
      fixture.componentInstance.confirmReset();
      expect(api.factoryReset).not.toHaveBeenCalled();
    });

    it('shows a message when the counts cannot be loaded', () => {
      setupReset();
      api.resetPreview.and.returnValue(throwError(() => ({ code: 'internal_error', status: 500 })));
      openDialog();
      expect(el('[data-testid="reset-preview-error"]')).not.toBeNull();
      expect(el('[data-testid="reset-counts"]')).toBeNull();
    });
  });
});
