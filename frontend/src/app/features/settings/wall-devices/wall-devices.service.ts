import { Injectable, inject } from '@angular/core';
import { Observable, map } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';

export type WallDeviceStatus = 'pending' | 'online' | 'offline';

/** A paired (or waiting) wall device as the settings page lists it. */
export interface WallDeviceView {
  id: string;
  name: string;
  status: WallDeviceStatus;
  paired_at: string | null;
  last_seen_at: string | null;
}

export interface WallDeviceCreated {
  id: string;
  name: string;
  pairing_key: string;
  expires_at: string;
}

export interface WallDeviceCreateRequest {
  name: string;
  exit_code: string;
}

/** Only the fields that change are sent. */
export interface WallDeviceUpdate {
  name?: string;
  exit_code?: string;
}

@Injectable({
  providedIn: 'root',
})
export class WallDevicesService {
  private api = inject(ApiClient);

  list(): Observable<WallDeviceView[]> {
    return this.api.get<{ devices: WallDeviceView[] }>('/api/v1/wall/devices').pipe(map((res) => res.devices));
  }

  create(body: WallDeviceCreateRequest): Observable<WallDeviceCreated> {
    return this.api.post<WallDeviceCreated>('/api/v1/wall/devices', body);
  }

  update(id: string, body: WallDeviceUpdate): Observable<WallDeviceView> {
    return this.api.patch<WallDeviceView>(`/api/v1/wall/devices/${encodeURIComponent(id)}`, body);
  }

  revoke(id: string): Observable<void> {
    return this.api.post<void>(`/api/v1/wall/devices/${encodeURIComponent(id)}/revoke`, {});
  }
}
