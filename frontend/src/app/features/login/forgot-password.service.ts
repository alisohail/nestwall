import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';

export interface ForgotPasswordResponse {
  /**
   * `child` when the user ID is a child account. `email_not_set_up` when no email is
   * set up for the account's family (or for any family, on the email route). Otherwise `accepted`.
   */
  status: 'accepted' | 'child' | 'email_not_set_up';
}

/**
 * Calls the public forgot-password endpoints. Only a child user ID gets a
 * different reply; parent and unknown IDs get the same one.
 */
@Injectable({ providedIn: 'root' })
export class ForgotPasswordService {
  private api = inject(ApiClient);

  requestReset(userId: string): Observable<ForgotPasswordResponse> {
    return this.api.post<ForgotPasswordResponse>('/api/v1/auth/forgot-password', { user_id: userId });
  }

  notifyParents(userId: string): Observable<ForgotPasswordResponse> {
    return this.api.post<ForgotPasswordResponse>('/api/v1/auth/forgot-password/notify-parents', { user_id: userId });
  }

  /** Parent path: the reply is the same whether or not the address is saved. */
  requestResetByEmail(email: string): Observable<ForgotPasswordResponse> {
    return this.api.post<ForgotPasswordResponse>('/api/v1/auth/forgot-password/email', { email });
  }

  /** Sets the new password with the token from the emailed link. Resolves with no body (204). */
  resetPassword(token: string, newPassword: string): Observable<void> {
    return this.api.post<void>('/api/v1/auth/reset-password', { token, new_password: newPassword });
  }
}
