import { Injectable, inject } from '@angular/core';
import { Observable, switchMap } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';
import { SetupService } from '../../../core/setup/setup.service';
import { WallSummary } from './wall-summary.models';

@Injectable({
  providedIn: 'root',
})
export class WallDisplayService {
  private api = inject(ApiClient);
  private setup = inject(SetupService);

  /**
   * Loads the family summary this paired display shows. Pass the display's own
   * coordinates only when the family has no weather location.
   */
  summary(deviceCoords?: { lat: number; lon: number }): Observable<WallSummary> {
    const query = deviceCoords
      ? `?${new URLSearchParams({ lat: String(deviceCoords.lat), lon: String(deviceCoords.lon) })}`
      : '';
    return this.api.get<WallSummary>(`/api/v1/wall/summary${query}`);
  }

  /** Picture for a family member, served to the paired display. */
  avatarUrl(avatarId: string, size: 96 | 256): string {
    return `/api/v1/wall/avatars/${avatarId}?size=${size}`;
  }

  /** Checks the exit code typed on the display. Completes without a value when it is right. */
  verifyExitCode(code: string): Observable<void> {
    return this.post('/api/v1/wall/exit/verify', { exit_code: code });
  }

  /** Unpairs this display. The server asks for the exit code again. */
  unpair(code: string): Observable<void> {
    return this.post('/api/v1/wall/unpair', { exit_code: code });
  }

  /** The CSRF token arrives on a response, so a display that has not loaded yet makes one GET first. */
  private post(url: string, body: unknown): Observable<void> {
    const request = () => this.api.post<void>(url, body);
    if (this.api.getCsrfToken()) return request();
    return this.setup.getStatus().pipe(switchMap(request));
  }
}
