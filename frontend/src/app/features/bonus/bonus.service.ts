import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import {
  BonusClaim,
  BonusScope,
  BonusTask,
  BonusTaskEditRequest,
  BonusTaskRequest,
  ListBonusTasksResponse,
} from './bonus.models';

@Injectable({ providedIn: 'root' })
export class BonusService {
  private api = inject(ApiClient);

  /** Parent view: the tasks of one tab (today, scheduled or past) with every claim. */
  list(scope: BonusScope = 'today'): Observable<ListBonusTasksResponse> {
    return this.api.get<ListBonusTasksResponse>(`/api/v1/bonus-tasks?scope=${scope}`);
  }

  create(body: BonusTaskRequest): Observable<BonusTask> {
    return this.api.post<BonusTask>('/api/v1/bonus-tasks', body);
  }

  /** Edits a task with no claims. Only the fields in body change. version is the task version. */
  edit(taskId: string, version: number, body: BonusTaskEditRequest): Observable<BonusTask> {
    return this.api.patch<BonusTask>(`/api/v1/bonus-tasks/${taskId}`, body, version);
  }

  /** Cancels a task that no child has claimed. version is the task version. */
  cancel(taskId: string, version: number): Observable<BonusTask> {
    return this.api.post<BonusTask>(`/api/v1/bonus-tasks/${taskId}/cancel`, {}, version);
  }

  /** Removes the child from a claimed, submitted or rejected claim. The task reopens. version is the claim version. */
  unassign(claimId: string, version: number): Observable<BonusClaim> {
    return this.api.post<BonusClaim>(`/api/v1/bonus-claims/${claimId}/unassign`, {}, version);
  }

  /** version is the claim version the decision is based on. A stale version fails with 412. */
  approve(claimId: string, version: number): Observable<BonusClaim> {
    return this.api.post<BonusClaim>(`/api/v1/bonus-claims/${claimId}/approve`, {}, version);
  }

  /** Sends a submitted claim back. The note is optional and up to 200 characters. */
  reject(claimId: string, version: number, note: string): Observable<BonusClaim> {
    return this.api.post<BonusClaim>(`/api/v1/bonus-claims/${claimId}/reject`, { decision_note: note }, version);
  }

  /** Child: takes a bonus task. A repeated request returns the same claim again. */
  claim(taskId: string): Observable<BonusClaim> {
    return this.api.post<BonusClaim>(`/api/v1/bonus-tasks/${taskId}/claim`, {});
  }

  /** Child: gives the claim back so others can take the task. version is the claim version. */
  release(taskId: string, version: number): Observable<BonusClaim> {
    return this.api.post<BonusClaim>(`/api/v1/bonus-tasks/${taskId}/release`, {}, version);
  }

  /** Child: marks the claim done. version is the claim version. */
  complete(taskId: string, version: number): Observable<BonusClaim> {
    return this.api.post<BonusClaim>(`/api/v1/bonus-tasks/${taskId}/complete`, {}, version);
  }
}
