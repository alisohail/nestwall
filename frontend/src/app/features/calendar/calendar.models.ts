export type CalendarItemKind = 'event' | 'task';

export type TaskCalendarStatus = 'scheduled' | 'past_due' | 'missed';

/** What a delete removes: one occurrence of a repeating event, or the whole event with its repeats. */
export type DeleteScope = 'occurrence' | 'series';

/** One occurrence in the range response. Event fields are set for events; task fields for task deadlines. */
export interface CalendarItem {
  key: string;
  kind: CalendarItemKind;
  title: string;
  occurrence_date: string;
  starts_at: string;
  ends_at: string;
  all_day: boolean;
  time_zone: string;
  /** Empty for family-wide items. */
  member_ids: string[];
  event_id?: string;
  /** The id of the calendar the event is on: a manual calendar, or an import. */
  calendar_id?: string;
  calendar_name?: string;
  /** Set for imported calendars. Local events take their colour from their people. */
  calendar_colour?: string;
  description?: string;
  location?: string;
  recurring?: boolean;
  edited?: boolean;
  task_id?: string;
  status?: TaskCalendarStatus;
}

export interface CalendarRange {
  from: string;
  to: string;
  time_zone: string;
  items: CalendarItem[];
  /** Every calendar the viewer can see at least one event from, whatever the window. */
  calendars: CalendarSummary[];
}

/** A calendar the viewer can see. Colour is omitted for the family's local calendar. */
export interface CalendarSummary {
  id: string;
  name: string;
  colour?: string;
}

/** Who an imported calendar is shown to. Parents only means the Parent role. */
export type ImportAudience = 'everyone' | 'specific' | 'parents';

/** How often a linked calendar is fetched again, in minutes. The server accepts only these values. */
export const REFRESH_CHOICES = [15, 30, 60, 360, 1440] as const;

export type RefreshMinutes = (typeof REFRESH_CHOICES)[number];

/** A calendar import. Its events are copies; deleting the import deletes them. Linked calendars also sync on a schedule. */
export interface CalendarImport {
  id: string;
  name: string;
  kind: 'file' | 'link';
  file_name: string | null;
  event_count: number;
  skipped_count: number;
  colour: string;
  audience: ImportAudience;
  show_on_wall: boolean;
  /** Only set when audience is specific. */
  member_ids: string[];
  refresh_minutes: RefreshMinutes;
  paused: boolean;
  last_synced_at: string | null;
  /** A code such as unavailable or too_large. Null after a successful sync. */
  last_sync_error: string | null;
  created_at: string;
}

/** Body for renaming or changing an import. name and colour are always sent. Other fields are optional. */
export interface ImportUpdate {
  name: string;
  colour: string;
  audience?: ImportAudience;
  member_ids?: string[];
  show_on_wall?: boolean;
  refresh_minutes?: RefreshMinutes;
  paused?: boolean;
}

/** A manual calendar, which holds events made in the app. The family keeps at least one. */
export interface CalendarSource {
  id: string;
  name: string;
  colour: string;
  event_count: number;
}

/** Body for creating or renaming a manual calendar. */
export interface CalendarSourceRequest {
  name: string;
  colour: string;
}

export interface EventException {
  occurrence_date: string;
  kind: string;
  title?: string;
  description?: string;
  location?: string;
  start_date?: string;
  start_time?: string;
  end_date?: string;
  end_time?: string;
}

/** A local event as the server returns it. version is sent back as If-Match on update. */
export interface EventDetail {
  id: string;
  title: string;
  description: string | null;
  location: string | null;
  all_day: boolean;
  time_zone: string;
  start_date: string;
  start_time: string | null;
  end_date: string;
  end_time: string | null;
  recurrence_rule: string | null;
  member_ids: string[];
  /** The manual calendar that holds the event. */
  calendar_id: string;
  /** Minutes before the start to remind the people on the event. The whole event shares them. */
  reminders: number[];
  version: number;
  exceptions: EventException[];
}

/** Body for creating or replacing a whole event. calendar_id is optional: the server uses the oldest calendar when it is omitted. */
export interface EventRequest {
  title: string;
  description: string | null;
  location: string | null;
  all_day: boolean;
  time_zone: string;
  start_date: string;
  start_time: string | null;
  end_date: string;
  end_time: string | null;
  recurrence_rule: string | null;
  member_ids: string[];
  calendar_id?: string;
  /** Minutes before the start to remind the people on the event. Replaces the stored list. */
  reminders: number[];
}

/** Body for changing one occurrence. Its people and repeat settings come from the series. */
export interface OccurrenceRequest {
  title: string;
  description: string | null;
  location: string | null;
  start_date: string;
  start_time: string | null;
  end_date: string;
  end_time: string | null;
}

/**
 * Client-side filter for the items already loaded. members null means everyone.
 * Items without members are family-wide and always pass the person filter.
 */
export interface CalendarFilter {
  members: string[] | null;
  /** Calendar ids switched off. Items without a calendar always pass. */
  hiddenCalendars: string[];
  tasks: boolean;
}

export const ALL_ITEMS: CalendarFilter = { members: null, hiddenCalendars: [], tasks: true };

export function passesFilter(item: CalendarItem, filter: CalendarFilter): boolean {
  if (item.kind === 'task' && !filter.tasks) return false;
  if (item.calendar_id && filter.hiddenCalendars.includes(item.calendar_id)) return false;
  if (filter.members === null || item.member_ids.length === 0) return true;
  return item.member_ids.some((id) => filter.members?.includes(id));
}
