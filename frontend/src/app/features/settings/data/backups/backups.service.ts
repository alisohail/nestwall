import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../../../core/api/api-client.service';

export type BackupKind = 'scheduled' | 'manual';

/** One archive in the backup history. `downloadable` is false when the file is gone or the run failed. */
export interface BackupRun {
  id: string;
  kind: BackupKind | string;
  status: 'ok' | 'failed' | 'running' | string;
  verified: boolean;
  encrypted: boolean;
  size_bytes: number;
  error_message?: string;
  started_at: string;
  downloadable: boolean;
}

export interface BackupStatus {
  last_good?: BackupRun;
  next_run_at: string;
  schedule_hour: number;
  keep_count: number;
  encrypt: boolean;
  passphrase_set: boolean;
  location: string;
  history: BackupRun[];
}

@Injectable({
  providedIn: 'root',
})
export class BackupsService {
  private api = inject(ApiClient);

  get(): Observable<BackupStatus> {
    return this.api.get<BackupStatus>('/api/v1/settings/backups');
  }

  /**
   * passphrase null keeps the stored passphrase, and a string replaces it.
   * Turning encryption on needs a passphrase unless one is already stored.
   */
  setEncryption(encrypt: boolean, passphrase: string | null): Observable<BackupStatus> {
    return this.api.put<BackupStatus>('/api/v1/settings/backups/encryption', { encrypt, passphrase });
  }

  /** scheduleHour is the local hour (0 to 23) of the daily backup. keepCount is how many of the newest archives to keep (1 to 60). */
  setSchedule(scheduleHour: number, keepCount: number): Observable<BackupStatus> {
    return this.api.put<BackupStatus>('/api/v1/settings/backups/schedule', { schedule_hour: scheduleHour, keep_count: keepCount });
  }

  runNow(): Observable<BackupStatus> {
    return this.api.post<BackupStatus>('/api/v1/settings/backups/run', {});
  }

  /** The server checks the account password first and returns password_incorrect if it is wrong. */
  download(id: string, password: string): Observable<Blob> {
    return this.api.postDownload(`/api/v1/settings/backups/${encodeURIComponent(id)}/download`, { password });
  }

  /** Replaces the live data with the archive, then the API restarts. The response arrives before the restart. */
  restore(id: string, password: string): Observable<RestoreResult> {
    return this.api.post<RestoreResult>(`/api/v1/settings/backups/${encodeURIComponent(id)}/restore`, { password });
  }

  /** Restores from a zip the parent provides. The server checks it the same way as a backup from the list. */
  restoreUpload(file: File, password: string): Observable<RestoreResult> {
    const body = new FormData();
    body.append('password', password);
    body.append('archive', file, file.name);
    return this.api.post<RestoreResult>('/api/v1/settings/backups/restore-upload', body);
  }

  /** Liveness probe used to wait for the API to come back after a restore. */
  live(): Observable<unknown> {
    return this.api.get<unknown>('/api/v1/health/live');
  }

  /** The exact counts a factory reset would remove. Shown in the dialog before anything is removed. */
  resetPreview(): Observable<ResetCounts> {
    return this.api.get<ResetCounts>('/api/v1/settings/reset/preview');
  }

  /** Takes a backup, then removes all family data. The server checks the typed word and the password first. */
  factoryReset(password: string, confirm: string): Observable<unknown> {
    return this.api.post<unknown>('/api/v1/settings/reset', { password, confirm });
  }
}

export interface RestoreResult {
  /** File name of the safety archive taken from the data before the restore. */
  safety_backup: string;
  restarting: boolean;
}

export interface ResetCounts {
  members: number;
  children: number;
  tasks: number;
  points_entries: number;
  events: number;
  avatars: number;
  wall_devices: number;
  api_tokens: number;
}
