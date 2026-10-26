import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { ParentDashboard } from './dashboard.models';

@Injectable({ providedIn: 'root' })
export class DashboardService {
  private api = inject(ApiClient);

  /** Parent home: today's band, counts, people, approvals and recent activity for the family's day. */
  parent(): Observable<ParentDashboard> {
    return this.api.get<ParentDashboard>('/api/v1/dashboard/parent');
  }
}
