import { Injectable, inject } from '@angular/core';
import { Observable, switchMap } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';
import { SetupService } from '../../../core/setup/setup.service';

export interface WallDevice {
  id: string;
  name: string;
  paired_at: string;
}

@Injectable({
  providedIn: 'root',
})
export class WallPairService {
  private api = inject(ApiClient);
  private setup = inject(SetupService);

  /** Pairs this display with the key typed on screen. */
  pair(key: string): Observable<WallDevice> {
    const request = () => this.api.post<WallDevice>('/api/v1/wall/pair', { key });
    if (this.api.getCsrfToken()) return request();
    // The CSRF token arrives on a response, so a fresh display makes one GET first.
    return this.setup.getStatus().pipe(switchMap(request));
  }
}
