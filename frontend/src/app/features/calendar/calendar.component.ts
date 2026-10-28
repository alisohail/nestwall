import { Component, DestroyRef, HostListener, OnInit, computed, inject, signal } from '@angular/core';
import { DOCUMENT } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { Subject, catchError, finalize, of, switchMap, tap } from 'rxjs';
import { AuthService } from '../../core/auth/auth.service';
import { DisplayZoneService } from '../../core/timezone/display-zone.service';
import { ApiError } from '../../core/api/api-client.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { IconComponent } from '../../shared/ui/icon/icon.component';
import { FamilyService, MemberDetail } from '../family/family.service';
import { todayIso } from '../tasks/tasks.format';
import { TasksService } from '../tasks/tasks.service';
import { DayDialogComponent } from './day-dialog/day-dialog.component';
import { EventDetailComponent } from './event-detail/event-detail.component';
import { EventDialogComponent } from './event-dialog/event-dialog.component';
import {
  GridDay,
  addDays,
  clockIn,
  dayTitle,
  monthGrid,
  monthTitle,
  shiftMonth,
  shortMonthLabels,
  weekDays,
  weekTitle,
  weekdayLabels,
} from './calendar.dates';
import { CalendarFilter, CalendarItem, CalendarSummary, DeleteScope, EventDetail, passesFilter } from './calendar.models';
import { CalendarService } from './calendar.service';

const MAX_PILLS = 3;
/** Week columns are taller than month cells, so they fit more before "+N more". */
const WEEK_PILLS = 6;

/** The backend sends the same name in Content-Disposition. */
const EXPORT_FILE_NAME = 'nestwall-family.ics';

export type CalendarView = 'day' | 'week' | 'month';

interface Focus {
  year: number;
  month: number;
}

interface Span {
  from: string;
  to: string;
}

/** A calendar chip in the filter bar. */
export interface CalendarChoice {
  id: string;
  name: string;
  colour: string;
}

export interface MonthPill {
  key: string;
  title: string;
  task: boolean;
  colour: string;
  item: CalendarItem;
  /** Set for events. Tasks have no event to edit. */
  eventId: string | null;
  /** The day this pill sits on. For a repeating event, this is the occurrence date. */
  date: string;
  recurring: boolean;
}

export interface MonthCell extends GridDay {
  today: boolean;
  /** The first few items, as shown on the day. */
  pills: MonthPill[];
  /** Every item on the day, for the day dialog. */
  all: MonthPill[];
  more: number;
}

/** Month view follows the mock. Day and week views are app-style lists and columns. */
@Component({
    selector: 'app-calendar',
    imports: [TranslocoModule, AvatarComponent, IconComponent, DayDialogComponent, EventDetailComponent, EventDialogComponent],
    templateUrl: './calendar.component.html',
    styleUrl: './calendar.component.scss'
})
export class CalendarComponent implements OnInit {
  private auth = inject(AuthService);
  private displayZone = inject(DisplayZoneService);
  private calendar = inject(CalendarService);
  private tasks = inject(TasksService);
  private family = inject(FamilyService);
  private destroyRef = inject(DestroyRef);
  private document = inject(DOCUMENT);
  private transloco = inject(TranslocoService);

  readonly isChild = this.auth.isChild;
  readonly zone = computed(() => this.auth.currentUser()?.family?.timezone);
  readonly today = todayIso(new Date(), this.zone());
  readonly weekdays = weekdayLabels();

  readonly focus = signal<Focus>(focusOf(this.today));
  readonly view = signal<CalendarView>('month');
  /** The date the day and week views show. Kept in step with focus, which the month view uses. */
  readonly anchor = signal(this.today);
  readonly title = computed(() => {
    switch (this.view()) {
      case 'day':
        return dayTitle(this.anchor());
      case 'week':
        return weekTitle(this.anchor());
      default:
        return monthTitle(this.focus().year, this.focus().month);
    }
  });
  /** Month and year picker opened from the heading. */
  readonly pickerOpen = signal(false);
  readonly pickerYear = signal(this.focus().year);
  readonly monthLabels = shortMonthLabels();

  readonly loading = signal(true);
  /** Bars per cell in the loading skeleton. A varied pattern makes the placeholder look like a real month. */
  readonly skeletonMonthPills = (i: number): number => [0, 2, 1, 0, 1, 3, 0][i % 7];
  readonly skeletonWeekPills = [2, 1, 3, 0, 2, 1, 1];
  readonly range = (n: number): number[] => Array.from({ length: n }, (_, i) => i);
  readonly failed = signal(false);
  readonly members = signal<MemberDetail[]>([]);
  private readonly items = signal<CalendarItem[]>([]);
  /** Members switched off in the person filter. */
  readonly hidden = signal<ReadonlySet<string>>(new Set());
  /** Whether the floating people filter is expanded. */
  readonly peopleOpen = signal(false);
  /** True when at least one member is switched off, so the filter is narrowing the calendar. */
  readonly filtered = computed(() => {
    const hidden = this.hidden();
    return this.members().some((m) => hidden.has(m.id));
  });
  /** Calendars switched off in the filter bar. Their items are hidden. */
  readonly hiddenCalendars = signal<ReadonlySet<string>>(new Set());
  /** Calendars the viewer can see, from the server, so each has a chip whatever month is on screen. */
  readonly calendarChoices = signal<ReadonlyMap<string, CalendarChoice>>(new Map());
  readonly calendarList = computed(() => [...this.calendarChoices().values()].sort((a, b) => a.name.localeCompare(b.name)));
  readonly showTasks = signal(false);

  /** Message key for a failed open of an event. */
  readonly openError = signal<string | null>(null);
  readonly exporting = signal(false);
  readonly exportError = signal<string | null>(null);
  readonly dialogOpen = signal(false);
  readonly dialogEvent = signal<EventDetail | null>(null);
  readonly dialogOccurrence = signal<string | null>(null);
  readonly dialogDate = signal('');
  /** The day whose "+N more" dialog is open. */
  readonly dayCell = signal<MonthCell | null>(null);
  /** The item whose detail dialog is open. */
  readonly detail = signal<MonthPill | null>(null);

  private readonly memberById = computed(() => new Map(this.members().map((m) => [m.id, m])));

  /** Stable across change detection, so the detail's people input does not change on every check. */
  readonly detailPeople = computed<MemberDetail[]>(() => {
    const d = this.detail();
    return d ? this.peopleOf(d.item) : [];
  });

  readonly filter = computed<CalendarFilter>(() => {
    const members = this.members();
    const hidden = this.hidden();
    return {
      // Until the member list loads, do not hide people.
      members: members.length ? members.filter((m) => !hidden.has(m.id)).map((m) => m.id) : null,
      hiddenCalendars: [...this.hiddenCalendars()],
      tasks: this.showTasks(),
    };
  });

  readonly cells = computed<MonthCell[]>(() => {
    const { year, month } = this.focus();
    const byDay = this.itemsByDay();
    return monthGrid(year, month).map((day) => this.toCell(day, byDay, MAX_PILLS));
  });

  readonly weekCells = computed<MonthCell[]>(() => {
    const byDay = this.itemsByDay();
    return weekDays(this.anchor()).map((day) => this.toCell(day, byDay, WEEK_PILLS));
  });

  /** Every item on the anchor day, already sorted with all-day items first. */
  readonly dayItems = computed<MonthPill[]>(() => (this.itemsByDay().get(this.anchor()) ?? []).map((item) => this.toPill(item)));

  private readonly load = new Subject<Span>();

  ngOnInit(): void {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe();

    // Children do not get the member list, so the people filter is hidden for them.
    if (!this.isChild()) {
      this.family
        .listMembers()
        .pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe({
          next: (res) => this.members.set(res.members),
          error: () => this.members.set([]),
        });
    }

    this.load
      .pipe(
        switchMap((span) => {
          this.loading.set(true);
          this.failed.set(false);
          return this.calendar.range(span.from, span.to, true).pipe(
            tap(() => this.loading.set(false)),
            catchError(() => {
              this.failed.set(true);
              this.loading.set(false);
              return of({ from: '', to: '', time_zone: '', items: [] as CalendarItem[], calendars: [] as CalendarSummary[] });
            }),
          );
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((res) => {
        this.items.set(res.items);
        if (!this.failed()) this.rememberCalendars(res.calendars);
      });

    this.load.next(this.visibleSpan());
  }

  move(delta: number): void {
    if (this.view() === 'month') {
      const { year, month } = this.focus();
      this.goTo(shiftMonth(year, month, delta));
      return;
    }
    this.setAnchor(addDays(this.anchor(), this.view() === 'week' ? delta * 7 : delta));
  }

  goToday(): void {
    if (this.view() === 'month') {
      this.goTo(focusOf(this.today));
      return;
    }
    this.setAnchor(this.today);
  }

  /** Switching from month keeps the day in view: today when it is in the focused month, otherwise the 1st. */
  setView(next: CalendarView): void {
    if (next === this.view()) return;
    if (this.view() === 'month') {
      this.anchor.set(this.anchorFor(this.focus()));
    }
    if (next === 'month') {
      this.focus.set(focusOf(this.anchor()));
    }
    this.view.set(next);
    this.reload();
  }

  togglePicker(): void {
    if (this.pickerOpen()) {
      this.closePicker();
      return;
    }
    this.pickerYear.set(this.focus().year);
    this.pickerOpen.set(true);
  }

  closePicker(): void {
    this.pickerOpen.set(false);
  }

  shiftPickerYear(delta: number): void {
    this.pickerYear.update((year) => year + delta);
  }

  pickMonth(month: number): void {
    this.closePicker();
    const year = this.pickerYear();
    if (this.view() === 'month') {
      this.goTo({ year, month });
      return;
    }
    this.setAnchor(`${year}-${pad2(month + 1)}-01`);
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.closePicker();
  }

  retry(): void {
    this.reload();
  }

  /** Only a caller with calendar.manage can create, edit or delete events. Children see the calendar read-only. */
  canManage(): boolean {
    return this.auth.can('calendar.manage');
  }

  /** Only a caller with calendar.view can export the whole family calendar. The server checks it again. */
  canExport(): boolean {
    return this.auth.can('calendar.view');
  }

  exportCalendar(): void {
    if (this.exporting()) return;
    this.exporting.set(true);
    this.exportError.set(null);
    this.calendar
      .exportFamily()
      .pipe(
        finalize(() => this.exporting.set(false)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (file) => this.saveFile(file),
        error: () => this.exportError.set('calendar.export.failed'),
      });
  }

  private saveFile(file: Blob): void {
    const url = URL.createObjectURL(file);
    const link = this.document.createElement('a');
    link.href = url;
    link.download = EXPORT_FILE_NAME;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url));
  }

  /** New events start on the anchor day in day and week views, otherwise on the focused month's anchor. */
  openNew(): void {
    const date = this.view() === 'month' ? this.anchorFor(this.focus()) : this.anchor();
    this.openError.set(null);
    this.dialogEvent.set(null);
    this.dialogOccurrence.set(null);
    this.dialogDate.set(date);
    this.dialogOpen.set(true);
  }

  /** Loads the latest version of the event before the dialog opens, so the save is based on it. */
  openEdit(pill: MonthPill): void {
    if (!pill.eventId) return;
    const occurrence = pill.recurring ? pill.date : null;
    this.openError.set(null);
    this.calendar.getEvent(pill.eventId).subscribe({
      next: (event) => {
        this.dialogEvent.set(event);
        this.dialogOccurrence.set(occurrence);
        this.dialogOpen.set(true);
      },
      error: (err: ApiError) => {
        this.openError.set(err.status === 404 ? 'calendar.event.errors.not_found' : 'calendar.event.errors.failed');
      },
    });
  }

  closeDialog(): void {
    this.dialogOpen.set(false);
  }

  openDay(cell: MonthCell): void {
    this.dayCell.set(cell);
  }

  /** Clock time for a timed item, or the all-day label. Tasks show their deadline. */
  timeOf(pill: MonthPill): string {
    return clockIn(pill.item.starts_at, pill.item.time_zone || this.displayZone.clockZone(this.zone()));
  }

  closeDay(): void {
    this.dayCell.set(null);
  }

  openDetail(pill: MonthPill): void {
    this.dayCell.set(null);
    this.detail.set(pill);
  }

  closeDetail(): void {
    this.detail.set(null);
  }

  /** Closes the detail first, so the edit dialog is the only one open. */
  editFromDetail(pill: MonthPill): void {
    this.detail.set(null);
    this.openEdit(pill);
  }

  /** An item with no people is for everyone, so every member is shown. */
  peopleOf(item: CalendarItem): MemberDetail[] {
    if (item.member_ids.length === 0) return this.members();
    return item.member_ids.map((id) => this.memberById().get(id)).filter((m): m is MemberDetail => !!m);
  }

  /** Loads the latest version, then removes one occurrence or the whole event with its repeats. */
  deleteEvent(pill: MonthPill, scope: DeleteScope = 'series'): void {
    if (!pill.eventId) return;
    const id = pill.eventId;
    const occurrence = scope === 'occurrence' && pill.recurring;
    this.detail.set(null);
    this.openError.set(null);
    this.calendar
      .getEvent(id)
      .pipe(
        switchMap((event) =>
          occurrence
            ? this.calendar.cancelOccurrence(id, pill.date, event.version)
            : this.calendar.deleteEvent(id, event.version),
        ),
      )
      .subscribe({
        next: () => this.reload(),
        error: () => this.openError.set('calendar.detail.delete_failed'),
      });
  }

  onSaved(): void {
    this.dialogOpen.set(false);
    this.reload();
  }

  /** A person's copy was removed (the confirm made the request). Closes the detail and reloads. */
  onTaskRemoved(): void {
    this.detail.set(null);
    this.openError.set(null);
    this.reload();
  }

  toggleMember(id: string): void {
    const next = new Set(this.hidden());
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    this.hidden.set(next);
  }

  toggleCalendar(id: string): void {
    this.hiddenCalendars.update((hidden) => {
      const next = new Set(hidden);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  toggleTasks(): void {
    this.showTasks.update((v) => !v);
  }

  private reload(): void {
    this.load.next(this.visibleSpan());
  }

  /** Month views load the whole grid, so the cells on the edges are filled too. */
  private goTo(focus: Focus): void {
    this.focus.set(focus);
    this.reload();
  }

  private setAnchor(iso: string): void {
    this.anchor.set(iso);
    this.focus.set(focusOf(iso));
    this.reload();
  }

  private visibleSpan(): Span {
    switch (this.view()) {
      case 'day':
        return { from: this.anchor(), to: this.anchor() };
      case 'week': {
        const days = weekDays(this.anchor());
        return { from: days[0].iso, to: days[6].iso };
      }
      default:
        return this.spanOf(this.focus());
    }
  }

  private anchorFor(focus: Focus): string {
    const current = focusOf(this.today);
    return focus.year === current.year && focus.month === current.month ? this.today : `${focus.year}-${pad2(focus.month + 1)}-01`;
  }

  private rememberCalendars(calendars: CalendarSummary[]): void {
    this.calendarChoices.set(
      new Map(calendars.map((c) => [c.id, { id: c.id, name: c.name, colour: c.colour ?? 'var(--nw-color-primary)' }])),
    );
  }

  private spanOf(focus: Focus): Span {
    const grid = monthGrid(focus.year, focus.month);
    return { from: grid[0].iso, to: grid[grid.length - 1].iso };
  }

  private toCell(day: GridDay, byDay: Map<string, CalendarItem[]>, cap: number): MonthCell {
    const all = (byDay.get(day.iso) ?? []).map((item) => this.toPill(item));
    return {
      ...day,
      today: day.iso === this.today,
      pills: all.slice(0, cap),
      all,
      more: Math.max(all.length - cap, 0),
    };
  }

  private itemsByDay(): Map<string, CalendarItem[]> {
    const filter = this.filter();
    const byDay = new Map<string, CalendarItem[]>();
    for (const item of this.items()) {
      if (!passesFilter(item, filter)) continue;
      const list = byDay.get(item.occurrence_date) ?? [];
      list.push(item);
      byDay.set(item.occurrence_date, list);
    }
    for (const list of byDay.values()) {
      list.sort((a, b) => Number(b.all_day) - Number(a.all_day) || a.starts_at.localeCompare(b.starts_at));
    }
    return byDay;
  }

  private toPill(item: CalendarItem): MonthPill {
    return {
      key: item.key,
      title: item.kind === 'birthday' ? this.birthdayTitle(item.title, item.age) : item.title,
      task: item.kind === 'task',
      colour: this.colourFor(item),
      item,
      eventId: item.kind === 'event' ? (item.event_id ?? null) : null,
      date: item.occurrence_date,
      recurring: !!item.recurring,
    };
  }

  private birthdayTitle(name: string, age?: number): string {
    return age
      ? this.transloco.translate('calendar.birthday.age', { name, age })
      : this.transloco.translate('calendar.birthday.title', { name });
  }

  private colourFor(item: CalendarItem): string {
    if (item.calendar_colour) return item.calendar_colour;
    const member = item.member_ids.map((id) => this.memberById().get(id)).find((m) => !!m);
    return member?.colour ?? 'var(--nw-color-primary)';
  }
}

function focusOf(iso: string): Focus {
  return { year: Number(iso.slice(0, 4)), month: Number(iso.slice(5, 7)) - 1 };
}

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}
