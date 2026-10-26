import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';

/** The family's notification switches and email template. `email_template` is the template in use. */
export interface NotificationSettings {
  in_app: boolean;
  browser: boolean;
  email: boolean;
  email_digest: boolean;
  digest_minutes: number;
  email_template: string;
}

export interface NotificationSettingsResponse {
  settings: NotificationSettings;
  default_template: string;
}

/** Save body. An empty `email_template` restores the built-in template. */
export type NotificationSettingsRequest = NotificationSettings;

@Injectable({
  providedIn: 'root',
})
export class NotificationsSettingsService {
  private api = inject(ApiClient);

  get(): Observable<NotificationSettingsResponse> {
    return this.api.get<NotificationSettingsResponse>('/api/v1/notifications/settings');
  }

  save(body: NotificationSettingsRequest): Observable<NotificationSettingsResponse> {
    return this.api.put<NotificationSettingsResponse>('/api/v1/notifications/settings', body);
  }

  preview(emailTemplate: string): Observable<{ html: string }> {
    return this.api.post<{ html: string }>('/api/v1/notifications/settings/preview', { email_template: emailTemplate });
  }
}
