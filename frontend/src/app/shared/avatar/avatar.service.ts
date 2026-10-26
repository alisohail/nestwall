import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';

export type AvatarSize = 96 | 256;

/** Square crop in source-image pixels. */
export interface CropRect {
  x: number;
  y: number;
  size: number;
}

export interface AvatarSettings {
  members_can_change_avatar: boolean;
  show_avatars_on_wall: boolean;
}

export interface UploadAvatarResponse {
  avatar_id: string;
}

@Injectable({ providedIn: 'root' })
export class AvatarService {
  private api = inject(ApiClient);

  url(avatarId: string, size: AvatarSize = 96): string {
    return `/api/v1/avatars/${avatarId}?size=${size}`;
  }

  upload(memberId: string, file: File, crop: CropRect): Observable<UploadAvatarResponse> {
    const body = new FormData();
    body.append('file', file, file.name);
    body.append('x', String(crop.x));
    body.append('y', String(crop.y));
    body.append('size', String(crop.size));
    return this.api.post<UploadAvatarResponse>(`/api/v1/family/members/${memberId}/avatar`, body);
  }

  remove(memberId: string): Observable<void> {
    return this.api.delete<void>(`/api/v1/family/members/${memberId}/avatar`);
  }

  getSettings(): Observable<AvatarSettings> {
    return this.api.get<AvatarSettings>('/api/v1/family/avatar-settings');
  }

  updateSettings(settings: AvatarSettings): Observable<void> {
    return this.api.put<void>('/api/v1/family/avatar-settings', settings);
  }
}
