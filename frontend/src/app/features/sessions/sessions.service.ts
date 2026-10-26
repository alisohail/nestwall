import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';

export interface SessionView {
  id: string;
  user_agent: string;
  created_at: string;
  expires_at: string;
  current: boolean;
}

export interface SessionListResponse {
  sessions: SessionView[];
}

@Injectable({ providedIn: 'root' })
export class SessionsService {
  private api = inject(ApiClient);

  listOwn(): Observable<SessionListResponse> {
    return this.api.get<SessionListResponse>('/api/v1/auth/sessions');
  }

  revokeOwn(sessionId: string): Observable<void> {
    return this.api.delete<void>(`/api/v1/auth/sessions/${sessionId}`);
  }

  revokeOthers(): Observable<void> {
    return this.api.post<void>('/api/v1/auth/sessions/revoke-others', {});
  }

  listForMember(memberId: string): Observable<SessionListResponse> {
    return this.api.get<SessionListResponse>(`/api/v1/family/members/${memberId}/sessions`);
  }

  revokeAllForMember(memberId: string): Observable<void> {
    return this.api.post<void>(`/api/v1/family/members/${memberId}/sessions/revoke-all`, {});
  }
}
