import { Injectable, inject } from '@angular/core';
import { ApiClient } from '../api/api-client.service';
import { Observable } from 'rxjs';

export interface SetupStatusResponse {
  setup_required: boolean;
  message?: string;
}

export interface SetupParentRequest {
  user_id: string;
  password: string;
  display_name: string;
  email: string;
  family_name: string;
  timezone: string;
  locale: string;
}

export interface SetupParentResponse {
  user: {
    id: string;
    user_id: string;
    display_name: string;
    is_child: boolean;
  };
  family_id: string;
  message: string;
}

export interface SetupRestoreResponse {
  restarting: boolean;
  safety_backup?: string;
}

@Injectable({ providedIn: 'root' })
export class SetupService {
  private api = inject(ApiClient);

  getStatus(): Observable<SetupStatusResponse> {
    return this.api.get<SetupStatusResponse>('/api/v1/setup/status');
  }

  createParent(req: SetupParentRequest): Observable<SetupParentResponse> {
    return this.api.post<SetupParentResponse>('/api/v1/setup/parent', req);
  }

  /**
   * Restores a backup zip on a fresh install instead of creating a parent. The server replaces the data and
   * restarts, so the response arrives before the restart. The archive passphrase is empty for an unencrypted backup.
   */
  restoreFirstRun(file: File, passphrase: string): Observable<SetupRestoreResponse> {
    const body = new FormData();
    body.append('archive', file, file.name);
    if (passphrase) body.append('passphrase', passphrase);
    return this.api.post<SetupRestoreResponse>('/api/v1/setup/restore', body);
  }

  /** Liveness probe used to wait for the API to come back after the restore restarts it. */
  live(): Observable<unknown> {
    return this.api.get<unknown>('/api/v1/health/live');
  }
}
