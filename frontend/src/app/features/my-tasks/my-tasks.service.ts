import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { Task } from '../tasks/tasks.models';
import { MyTasksPage, MyTasksResponse } from './my-tasks.models';

/** Copies per page for the Coming up and Done lists. */
export const MY_TASKS_PAGE_SIZE = 20;

@Injectable({ providedIn: 'root' })
export class MyTasksService {
  private api = inject(ApiClient);

  /** The signed-in child's own copies, streak and sections for the Today and My tasks screens. */
  mine(): Observable<MyTasksResponse> {
    return this.api.get<MyTasksResponse>('/api/v1/me/tasks');
  }

  /** One page of the copies coming up, earliest first. Pass the previous page's next_cursor to continue. */
  coming(cursor: string | null = null): Observable<MyTasksPage> {
    return this.api.get<MyTasksPage>(this.pageUrl('coming', cursor));
  }

  /** One page of the finished copies, newest first. Pass the previous page's next_cursor to continue. */
  done(cursor: string | null = null): Observable<MyTasksPage> {
    return this.api.get<MyTasksPage>(this.pageUrl('done', cursor));
  }

  private pageUrl(list: 'coming' | 'done', cursor: string | null): string {
    const params = new URLSearchParams({ limit: String(MY_TASKS_PAGE_SIZE) });
    if (cursor) params.set('cursor', cursor);
    return `/api/v1/me/tasks/${list}?${params.toString()}`;
  }

  /** Ticks or unticks one step of the copy's checklist. version is the task version the tick is based on. */
  tickChecklist(taskId: string, itemId: string, done: boolean, version: number): Observable<Task> {
    return this.api.put<Task>(`/api/v1/tasks/${taskId}/checklist/${itemId}`, { done }, version);
  }

  /** Takes a submitted copy back to to do before a parent checks it. version is the task version the change is based on. */
  withdraw(taskId: string, version: number): Observable<Task> {
    return this.api.post<Task>(`/api/v1/tasks/${taskId}/withdraw`, {}, version);
  }
}
