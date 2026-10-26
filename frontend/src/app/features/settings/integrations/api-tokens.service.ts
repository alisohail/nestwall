import { Injectable, inject } from '@angular/core';
import { Observable, map } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';

/** Scopes a token can carry. Each one reads a single area of family data. */
export const API_TOKEN_SCOPES = ['tasks:read', 'calendar:read', 'points:read', 'rewards:read'] as const;
export type ApiTokenScope = (typeof API_TOKEN_SCOPES)[number];

/** Lifetimes the server accepts, in days. */
export const API_TOKEN_EXPIRY_DAYS = [30, 90, 365] as const;

/** A token as the settings page lists it. The raw value is never returned after creation. */
export interface ApiTokenView {
  id: string;
  name: string;
  scopes: string[];
  token_hint: string;
  expires_at: string;
  last_used_at: string | null;
  created_at: string;
}

/** Returned once by create. The token is shown to the user and never stored in clear. */
export interface ApiTokenCreated extends ApiTokenView {
  token: string;
}

export interface ApiTokenCreateRequest {
  name: string;
  scopes: string[];
  expires_in_days: number;
}

@Injectable({
  providedIn: 'root',
})
export class ApiTokensService {
  private api = inject(ApiClient);

  list(): Observable<ApiTokenView[]> {
    return this.api.get<{ items: ApiTokenView[] }>('/api/v1/settings/api-tokens').pipe(map((res) => res.items));
  }

  create(body: ApiTokenCreateRequest): Observable<ApiTokenCreated> {
    return this.api.post<ApiTokenCreated>('/api/v1/settings/api-tokens', body);
  }

  revoke(id: string): Observable<void> {
    return this.api.post<void>(`/api/v1/settings/api-tokens/${encodeURIComponent(id)}/revoke`, {});
  }
}
