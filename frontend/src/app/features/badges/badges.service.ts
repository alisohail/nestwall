import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import {
  BadgeAward,
  BadgeAwardRequest,
  BadgeConfig,
  BadgeGroupKey,
  BadgeSettings,
  CustomBadge,
  CustomBadgeRequest,
  MyBadges,
} from './badges.models';

@Injectable({ providedIn: 'root' })
export class BadgesService {
  private api = inject(ApiClient);

  /** The switches, the six built-in groups and the custom badges. The version is the ETag. */
  get(): Observable<BadgeConfig> {
    return this.api.get<BadgeConfig>('/api/v1/badges');
  }

  /** Saves the family switches. The settings version is sent as If-Match. */
  saveSettings(settings: BadgeSettings, version: number): Observable<BadgeConfig> {
    return this.api.put<BadgeConfig>('/api/v1/badges/settings', settings, version);
  }

  /** Switches every badge in a built-in group on or off. The settings version is sent as If-Match. */
  setGroup(group: BadgeGroupKey, enabled: boolean, version: number): Observable<BadgeConfig> {
    return this.api.put<BadgeConfig>(`/api/v1/badges/groups/${group}`, { enabled }, version);
  }

  createCustom(body: CustomBadgeRequest): Observable<CustomBadge> {
    return this.api.post<CustomBadge>('/api/v1/badges/custom', body);
  }

  /** Edits a custom badge. The badge's own version is sent as If-Match. */
  updateCustom(id: string, body: CustomBadgeRequest, version: number): Observable<CustomBadge> {
    return this.api.put<CustomBadge>(`/api/v1/badges/custom/${id}`, body, version);
  }

  /** The signed-in child's own badges. Only the caller's records are returned. */
  mine(): Observable<MyBadges> {
    return this.api.get<MyBadges>('/api/v1/me/badges');
  }

  /** Gives a custom badge to a child. The same key makes a retry safe. */
  award(body: BadgeAwardRequest, idempotencyKey: string): Observable<BadgeAward> {
    return this.api.post<BadgeAward>('/api/v1/badges/awards', body, undefined, { 'Idempotency-Key': idempotencyKey });
  }
}
