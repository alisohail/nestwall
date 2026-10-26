import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';

export type EmailSecurity = 'starttls' | 'tls';

/** The family's SMTP setting, without the password. `ready` is true when the host and sender are set, so email can be sent. */
export interface EmailSettings {
  host: string;
  port: number;
  security: EmailSecurity;
  username: string;
  from_address: string;
  from_name: string;
  send_limit_per_hour: number;
  password_set: boolean;
  ready: boolean;
}

/**
 * Save body. `password` null keeps the saved password, an empty string clears it,
 * and any other value replaces it.
 */
export interface EmailSettingsRequest extends Omit<EmailSettings, 'password_set' | 'ready'> {
  password: string | null;
}

@Injectable({
  providedIn: 'root',
})
export class EmailSettingsService {
  private api = inject(ApiClient);

  get(): Observable<EmailSettings> {
    return this.api.get<EmailSettings>('/api/v1/settings/email');
  }

  save(body: EmailSettingsRequest): Observable<EmailSettings> {
    return this.api.put<EmailSettings>('/api/v1/settings/email', body);
  }

  sendTest(): Observable<void> {
    return this.api.post<void>('/api/v1/settings/email/test', {});
  }
}
