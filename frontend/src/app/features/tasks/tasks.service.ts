import { Injectable, inject } from "@angular/core";
import { Observable } from "rxjs";
import { ApiClient } from "../../core/api/api-client.service";
import {
  ListTaskCategoriesResponse,
  ListTaskTemplatesResponse,
  ListTasksResponse,
  RepeatCreateRequest,
  RemoveScope,
  RepeatCreated,
  Task,
  TaskCategory,
  TaskCategoryRequest,
  TaskListParams,
  TaskPatchRequest,
  TaskRequest,
  TaskSettingsRequest,
  TaskSettingsResponse,
  TaskTemplate,
  TaskTemplateRequest,
  TemplateKind,
} from "./tasks.models";

@Injectable({ providedIn: "root" })
export class TasksService {
  private api = inject(ApiClient);

  list(params: TaskListParams = {}): Observable<ListTasksResponse> {
    const query = new URLSearchParams();
    if (params.member) query.set("member", params.member);
    if (params.status) query.set("status", params.status);
    if (params.category) query.set("category", params.category);
    if (params.due_from) query.set("due_from", params.due_from);
    if (params.due_to) query.set("due_to", params.due_to);
    if (params.cursor) query.set("cursor", params.cursor);
    if (params.limit) query.set("limit", String(params.limit));
    if (params.archived) query.set("archived", "true");
    if (params.expired) query.set("expired", "true");
    const qs = query.toString();
    return this.api.get<ListTasksResponse>(
      `/api/v1/tasks${qs ? `?${qs}` : ""}`,
    );
  }

  get(id: string, memberId?: string): Observable<Task> {
    const qs = memberId ? `?member_id=${encodeURIComponent(memberId)}` : "";
    return this.api.get<Task>(`/api/v1/tasks/${id}${qs}`);
  }

  create(body: TaskRequest): Observable<Task> {
    return this.api.post<Task>("/api/v1/tasks", body);
  }

  createRepeat(body: RepeatCreateRequest): Observable<RepeatCreated> {
    return this.api.post<RepeatCreated>("/api/v1/task-definitions", body);
  }

  /** version is the task version the edit was based on. A stale version fails with 412. */
  update(
    id: string,
    body: TaskPatchRequest,
    version: number,
  ): Observable<Task> {
    return this.api.patch<Task>(`/api/v1/tasks/${id}`, body, version);
  }

  complete(id: string, memberId: string): Observable<Task> {
    return this.api.post<Task>(`/api/v1/tasks/${id}/complete`, {
      member_id: memberId,
    });
  }

  /** Approves a submitted copy. version is optional; when given it is sent as If-Match. */
  approve(id: string, memberId: string, version?: number): Observable<Task> {
    return this.api.post<Task>(
      `/api/v1/tasks/${id}/approve`,
      { member_id: memberId },
      version,
    );
  }

  /** Sends a submitted copy back with a required note. version is optional; when given it is sent as If-Match. */
  reject(
    id: string,
    memberId: string,
    note: string,
    version?: number,
  ): Observable<Task> {
    return this.api.post<Task>(
      `/api/v1/tasks/${id}/reject`,
      { member_id: memberId, note },
      version,
    );
  }

  skip(id: string, memberId: string, version?: number): Observable<Task> {
    return this.api.post<Task>(
      `/api/v1/tasks/${id}/skip`,
      { member_id: memberId },
      version,
    );
  }

  addChecklistItem(
    id: string,
    text: string,
    version?: number,
  ): Observable<Task> {
    return this.api.post<Task>(
      `/api/v1/tasks/${id}/checklist`,
      { text },
      version,
    );
  }

  /** Ticks or unticks one step. memberId names the copy to tick; leave it out to tick the signed-in member's own copy. */
  tickChecklist(
    id: string,
    item: string,
    done: boolean,
    version: number,
    memberId?: string,
  ): Observable<Task> {
    return this.api.put<Task>(
      `/api/v1/tasks/${id}/checklist/${item}`,
      memberId ? { done, member_id: memberId } : { done },
      version,
    );
  }

  renameChecklistItem(
    id: string,
    item: string,
    text: string,
    version: number,
  ): Observable<Task> {
    return this.api.patch<Task>(
      `/api/v1/tasks/${id}/checklist/${item}`,
      { text },
      version,
    );
  }

  removeChecklistItem(
    id: string,
    item: string,
    version: number,
  ): Observable<Task> {
    return this.api.delete<Task>(
      `/api/v1/tasks/${id}/checklist/${item}`,
      version,
    );
  }

  categories(): Observable<ListTaskCategoriesResponse> {
    return this.api.get<ListTaskCategoriesResponse>(
      "/api/v1/family/task-categories",
    );
  }

  settings(): Observable<TaskSettingsResponse> {
    return this.api.get<TaskSettingsResponse>("/api/v1/family/task-settings");
  }

  /** Lists templates of one kind. Regular is the default. */
  templates(
    cursor: string | null = null,
    kind: TemplateKind = "regular",
  ): Observable<ListTaskTemplatesResponse> {
    const query = new URLSearchParams({ kind });
    if (cursor) query.set("cursor", cursor);
    return this.api.get<ListTaskTemplatesResponse>(
      `/api/v1/task-templates?${query.toString()}`,
    );
  }

  createTemplate(body: TaskTemplateRequest): Observable<TaskTemplate> {
    return this.api.post<TaskTemplate>("/api/v1/task-templates", body);
  }

  /** Partial update. Absent fields keep their value; a null field clears it. version is the version the edit was based on. */
  updateTemplate(
    id: string,
    body: Partial<TaskTemplateRequest>,
    version: number,
  ): Observable<TaskTemplate> {
    return this.api.patch<TaskTemplate>(
      `/api/v1/task-templates/${id}`,
      body,
      version,
    );
  }

  /** Archives the task for everyone. Archived tasks leave the list and cannot change; history is kept. version is sent as If-Match. */
  archive(id: string, version: number): Observable<Task> {
    return this.api.post<Task>(`/api/v1/tasks/${id}/archive`, {}, version);
  }

  /** Removes one person's copy. scope "occurrence" is one day; "series" is that person's upcoming copies and their place in a repeating task. The last person archives the task. version is sent as If-Match. */
  removePerson(
    id: string,
    version: number,
    memberId: string,
    scope: RemoveScope,
  ): Observable<Task> {
    return this.api.post<Task>(
      `/api/v1/tasks/${id}/remove-person`,
      { member_id: memberId, scope },
      version,
    );
  }

  /** Permanently deletes an archived task, every person's copy and every repeat. Points already earned are kept. version is sent as If-Match. */
  deleteTask(id: string, version: number): Observable<void> {
    return this.api.delete<void>(`/api/v1/tasks/${id}`, version);
  }

  /** Restores an archived task to its state before archiving. Only whole-task archives can be restored. version is sent as If-Match. */
  restore(id: string, version: number): Observable<Task> {
    return this.api.post<Task>(`/api/v1/tasks/${id}/unarchive`, {}, version);
  }

  deleteTemplate(id: string, version: number): Observable<void> {
    return this.api.delete<void>(`/api/v1/task-templates/${id}`, version);
  }

  createCategory(body: TaskCategoryRequest): Observable<TaskCategory> {
    return this.api.post<TaskCategory>("/api/v1/family/task-categories", body);
  }

  updateCategory(
    id: string,
    body: TaskCategoryRequest,
    version: number,
  ): Observable<TaskCategory> {
    return this.api.put<TaskCategory>(
      `/api/v1/family/task-categories/${id}`,
      body,
      version,
    );
  }

  updateSettings(
    body: TaskSettingsRequest,
    version: number,
  ): Observable<TaskSettingsResponse> {
    return this.api.put<TaskSettingsResponse>(
      "/api/v1/family/task-settings",
      body,
      version,
    );
  }
}
