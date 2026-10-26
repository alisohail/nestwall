import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { CalendarImport, CalendarRange, CalendarSource, CalendarSourceRequest, EventDetail, EventRequest, ImportUpdate, OccurrenceRequest } from './calendar.models';

@Injectable({
  providedIn: 'root',
})
export class CalendarService {
  private api = inject(ApiClient);

  /** Both dates are YYYY-MM-DD and inclusive. Task deadlines are included only when tasks is true. */
  range(from: string, to: string, tasks: boolean): Observable<CalendarRange> {
    return this.api.get<CalendarRange>(`/api/v1/calendar/range?from=${from}&to=${to}&tasks=${tasks}`);
  }

  /** The whole family calendar as an iCalendar file. Needs calendar.view. */
  exportFamily(): Observable<Blob> {
    return this.api.download('/api/v1/calendar/export.ics');
  }

  createEvent(body: EventRequest): Observable<EventDetail> {
    return this.api.post<EventDetail>('/api/v1/calendar/events', body);
  }

  getEvent(id: string): Observable<EventDetail> {
    return this.api.get<EventDetail>(`/api/v1/calendar/events/${id}`);
  }

  /** version is the event version the dialog loaded. The server rejects the save if it has changed. */
  updateEvent(id: string, body: EventRequest, version: number): Observable<EventDetail> {
    return this.api.put<EventDetail>(`/api/v1/calendar/events/${id}`, body, version);
  }

  /** version is the event version the caller loaded. The server rejects the delete if it has changed. Repeats go with the event. */
  deleteEvent(id: string, version: number): Observable<void> {
    return this.api.delete<void>(`/api/v1/calendar/events/${id}`, version);
  }

  listImports(): Observable<{ items: CalendarImport[] }> {
    return this.api.get<{ items: CalendarImport[] }>('/api/v1/calendar/imports');
  }

  /** The form carries name, file, colour, audience and repeated member_ids. */
  createImport(form: FormData): Observable<CalendarImport> {
    return this.api.post<CalendarImport>('/api/v1/calendar/imports', form);
  }

  /** Deletes the import and every event it created. */
  deleteImport(id: string): Observable<void> {
    return this.api.delete<void>(`/api/v1/calendar/imports/${id}`);
  }

  /** Changes an import's settings. Its events show the new name and colour without being rewritten. */
  updateImport(id: string, body: ImportUpdate): Observable<void> {
    return this.api.patch<void>(`/api/v1/calendar/imports/${id}`, body);
  }

  /** The manual calendars. The first one is created on first use. */
  listSources(): Observable<{ items: CalendarSource[] }> {
    return this.api.get<{ items: CalendarSource[] }>('/api/v1/calendar/sources');
  }

  createSource(body: CalendarSourceRequest): Observable<CalendarSource> {
    return this.api.post<CalendarSource>('/api/v1/calendar/sources', body);
  }

  /** Renames a manual calendar and sets its colour. */
  updateSource(id: string, body: CalendarSourceRequest): Observable<CalendarSource> {
    return this.api.patch<CalendarSource>(`/api/v1/calendar/sources/${id}`, body);
  }

  /** Deletes a manual calendar and its events. The last remaining one cannot be deleted (409 last_calendar). */
  deleteSource(id: string): Observable<void> {
    return this.api.delete<void>(`/api/v1/calendar/sources/${id}`);
  }

  /** Fetches a linked calendar now. Resolves with the import; last_sync_error is set when the fetch failed. */
  syncImport(id: string): Observable<CalendarImport> {
    return this.api.post<CalendarImport>(`/api/v1/calendar/imports/${id}/sync`, {});
  }

  /** Changes one occurrence (date is YYYY-MM-DD, the original date of that occurrence). */
  editOccurrence(id: string, date: string, body: OccurrenceRequest, version: number): Observable<void> {
    return this.api.put<void>(`/api/v1/calendar/events/${id}/occurrences/${date}`, body, version);
  }

  /** Removes one occurrence and keeps the rest of the repeats. version is the event version the caller loaded. */
  cancelOccurrence(id: string, date: string, version: number): Observable<void> {
    return this.api.delete<void>(`/api/v1/calendar/events/${id}/occurrences/${date}`, version);
  }
}
