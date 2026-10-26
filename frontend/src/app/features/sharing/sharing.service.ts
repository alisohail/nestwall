import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { ShareRequest, ShareResult, ShareSettings, ShareSettingsInput, ShareSummary } from './sharing.models';

@Injectable({ providedIn: 'root' })
export class SharingService {
  private api = inject(ApiClient);

  /** Sharing rules, the caller's balance and daily numbers, and the siblings they may share with. */
  summary(): Observable<ShareSummary> {
    return this.api.get<ShareSummary>('/api/v1/points/share');
  }

  /** Saves the family sharing rules. The version is sent as If-Match so stale saves are refused. */
  saveSettings(input: ShareSettingsInput, version: number): Observable<ShareSettings> {
    return this.api.put<ShareSettings>('/api/v1/points/share/settings', input, version);
  }

  /** Sends points to a sibling. The same key makes a retry safe. */
  share(body: ShareRequest, idempotencyKey: string): Observable<ShareResult> {
    return this.api.post<ShareResult>('/api/v1/points/share', body, undefined, { 'Idempotency-Key': idempotencyKey });
  }
}
