import { AfterViewInit, Component, DestroyRef, ElementRef, HostListener, OnInit, computed, inject, input, output, signal, viewChild, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { Observable } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { todayIso } from '../../tasks/tasks.format';
import { RepeatBuilderComponent } from '../../tasks/task-form/repeat-builder/repeat-builder.component';
import { RepeatState, buildRule, defaultRepeat, repeatProblem } from '../../tasks/task-form/recurrence';
import { CalendarImport, CalendarSource, EventDetail, EventException, EventRequest, OccurrenceRequest } from '../calendar.models';
import { CalendarService } from '../calendar.service';
import { browserZone, daysBetween, endDateFor, startInstantMs, stateFromRule } from './event-form';

type Scope = 'occurrence' | 'series';
type ReminderUnit = 'minutes' | 'hours' | 'days';

const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/;
const CLOCK = /^([01]\d|2[0-3]):[0-5]\d$/;
const DEFAULT_START = '09:00';
const DEFAULT_END = '10:00';
const UNIT_MINUTES: Record<ReminderUnit, number> = { minutes: 1, hours: 60, days: 1440 };
/** Quick picks, in minutes before the start. */
const REMINDER_PRESETS = [0, 5, 10, 15, 30, 60, 1440];

function targetValue(event: Event): string {
  return (event.target as HTMLInputElement | HTMLSelectElement).value;
}

/** Message key for a failed save. Field errors from the server name the field that was wrong. */
function errorKey(err: ApiError): string {
  if (err.status === 412) return 'calendar.event.errors.conflict';
  if (err.status === 404) return 'calendar.event.errors.not_found';
  if (err.code === 'validation_failed') {
    const field = err.params?.['field'];
    const reason = err.params?.['reason'];
    if (reason === 'before_start') return 'calendar.event.errors.end_before_start';
    switch (field) {
      case 'title':
        return 'calendar.event.errors.title';
      case 'start_date':
      case 'end_date':
        return 'calendar.event.errors.date';
      case 'start_time':
      case 'end_time':
        return 'calendar.event.errors.time';
      case 'recurrence_rule':
        return 'calendar.event.errors.repeat';
      case 'member_ids':
        return 'calendar.event.errors.people';
      case 'reminders':
        return 'calendar.event.errors.reminders';
    }
  }
  return 'calendar.event.errors.failed';
}

/**
 * New event and edit dialog, as in the mock. Editing a repeating event offers
 * "This occurrence" (title, date and time only) or "Whole series".
 */
@Component({
    selector: 'app-event-dialog',
    imports: [TranslocoModule, IconComponent, RepeatBuilderComponent, AvatarComponent],
    templateUrl: './event-dialog.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './event-dialog.component.scss'
})
export class EventDialogComponent implements OnInit, AfterViewInit {
  private calendar = inject(CalendarService);
  private family = inject(FamilyService);
  private destroyRef = inject(DestroyRef);
  private transloco = inject(TranslocoService);
  /** The event being edited. null creates a new one. */
  readonly event = input<EventDetail | null>(null);
  /** The date of the occurrence that was opened, for a repeating event. */
  readonly occurrence = input<string | null>(null);
  /** Date to prefill for a new event (YYYY-MM-DD). */
  readonly defaultDate = input('');
  /** The family zone. Falls back to the viewer's zone. */
  readonly zone = input<string | undefined>(undefined);

  readonly saved = output<void>();
  readonly closed = output<void>();

  private readonly titleInput = viewChild<ElementRef<HTMLInputElement>>('titleInput');

  readonly members = signal<MemberDetail[]>([]);
  /** The manual calendars an event can be placed on. */
  readonly calendars = signal<CalendarSource[]>([]);
  /** Imported calendars an event can be placed on. */
  readonly imports = signal<CalendarImport[]>([]);
  readonly fileImports = computed(() => this.imports().filter((i) => i.kind === 'file'));
  /** Linked calendars take synced events only, so one is listed only when it is already the event's calendar. */
  readonly linkedImports = computed(() => this.imports().filter((i) => i.kind === 'link' && i.id === this.calendarId()));
  /** The calendar the event is on. A new event starts on the oldest calendar. */
  readonly calendarId = signal('');
  /** An import takes its people from its own audience, so the people picker is locked. */
  readonly selectedImport = computed(() => this.imports().find((i) => i.id === this.calendarId()) ?? null);
  readonly title = signal('');
  readonly location = signal('');
  readonly date = signal('');
  readonly allDay = signal(false);
  readonly startTime = signal(DEFAULT_START);
  readonly endTime = signal(DEFAULT_END);
  readonly memberIds = signal<string[]>([]);
  /** Minutes before the start. Null means no reminder. Shared by the whole event, so an occurrence edit leaves it alone. */
  readonly reminder = signal<number | null>(null);
  /** True when the reminder came from the custom amount rather than a preset chip. */
  readonly customActive = signal(false);
  readonly reminderList = computed(() => {
    const minutes = this.reminder();
    return minutes === null ? [] : [minutes];
  });
  readonly reminderPresets = REMINDER_PRESETS;
  readonly reminderUnits: ReminderUnit[] = ['minutes', 'hours', 'days'];
  readonly customAmount = signal('');
  readonly customUnit = signal<ReminderUnit>('minutes');
  readonly repeatState = signal<RepeatState>(defaultRepeat(todayIso(new Date(), 'UTC')));
  /** A stored rule the repeat builder cannot rebuild. It is saved unchanged until the user stops repeating. */
  readonly customRule = signal<string | null>(null);
  /** The date the repeat counts from. Keeps the last valid date while the date field is being typed. */
  readonly anchor = signal('');
  readonly scope = signal<Scope>('series');
  readonly saving = signal(false);
  readonly error = signal<string | null>(null);

  readonly isEdit = computed(() => this.event() !== null);
  readonly showScope = computed(() => !!this.event()?.recurrence_rule && this.occurrence() !== null);
  readonly occurrenceMode = computed(() => this.showScope() && this.scope() === 'occurrence');

  // Set once from the event. The form fields above hold what the user sees.
  private timeZone = '';
  private version = 0;
  private description: string | null = null;
  private spanDays = 0;

  ngOnInit(): void {
    this.family
      .listMembers()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.members.set(res.members),
        error: () => this.members.set([]),
      });

    this.calendar
      .listSources()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.calendars.set(res.items);
          if (!this.calendarId()) this.calendarId.set(res.items[0]?.id ?? '');
        },
        error: () => this.calendars.set([]),
      });

    this.calendar
      .listImports()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.imports.set(res.items),
        error: () => this.imports.set([]),
      });

    const event = this.event();
    if (event) {
      this.timeZone = event.time_zone;
      this.version = event.version;
      this.calendarId.set(event.calendar_id);
      this.scope.set(this.showScope() ? 'occurrence' : 'series');
      this.fill(event, this.scope());
    } else {
      this.timeZone = this.zone() ?? browserZone();
      this.date.set(this.defaultDate() || todayIso(new Date(), this.timeZone));
      this.anchorOn(this.date());
      this.repeatState.set(defaultRepeat(this.date()));
    }
  }

  ngAfterViewInit(): void {
    this.titleInput()?.nativeElement.focus();
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.close();
  }

  onCalendar(e: Event): void {
    this.calendarId.set(targetValue(e));
  }

  onTitle(e: Event): void {
    this.title.set(targetValue(e));
  }

  onLocation(e: Event): void {
    this.location.set(targetValue(e));
  }

  onDate(e: Event): void {
    this.date.set(targetValue(e));
    if (!this.occurrenceMode()) this.anchorOn(this.date());
  }

  onStart(e: Event): void {
    this.startTime.set(targetValue(e));
  }

  onEnd(e: Event): void {
    this.endTime.set(targetValue(e));
  }

  onAllDay(e: Event): void {
    this.allDay.set((e.target as HTMLInputElement).checked);
  }

  /** Drops a stored rule the builder could not express, so the event no longer repeats. */
  stopRepeat(): void {
    this.customRule.set(null);
    this.repeatState.update((state) => ({ ...state, freq: 'none' }));
  }

  togglePerson(id: string): void {
    this.memberIds.update((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]));
  }

  isPicked(id: string): boolean {
    return this.memberIds().includes(id);
  }

  /** A preset replaces any other reminder and clears the custom amount. Clicking the selected preset clears it. */
  togglePreset(minutes: number): void {
    const clearing = this.reminder() === minutes && !this.customActive();
    this.reminder.set(clearing ? null : minutes);
    this.customActive.set(false);
    this.customAmount.set('');
  }

  onCustomAmount(e: Event): void {
    this.customAmount.set(targetValue(e));
    this.applyCustom();
  }

  onCustomUnit(e: Event): void {
    this.customUnit.set(targetValue(e) as ReminderUnit);
    this.applyCustom();
  }

  /** A valid custom amount replaces the reminder and clears the presets. An empty or invalid amount clears it. */
  private applyCustom(): void {
    const amount = Number(this.customAmount());
    const valid = Number.isInteger(amount) && amount >= 1;
    this.customActive.set(valid);
    this.reminder.set(valid ? amount * UNIT_MINUTES[this.customUnit()] : null);
  }

  reminderLabel(minutes: number): string {
    if (minutes === 0) return this.transloco.translate('calendar.event.reminder_at_start');
    if (minutes % 1440 === 0) return this.transloco.translate('calendar.event.reminder_days', { count: minutes / 1440 });
    if (minutes % 60 === 0) return this.transloco.translate('calendar.event.reminder_hours', { count: minutes / 60 });
    return this.transloco.translate('calendar.event.reminder_minutes', { count: minutes });
  }

  /**
   * How many reminders are already past for the start on the form. The server drops those when a one-off
   * event is saved. A repeating event keeps them, because later occurrences can still use them.
   */
  reminderIsPast(): boolean {
    const minutes = this.reminder();
    if (minutes === null || !ISO_DATE.test(this.date()) || this.ruleToSave() !== null) return false;
    const start = startInstantMs(this.date(), this.allDay() ? null : this.startTime(), this.timeZone);
    return start - minutes * 60000 <= Date.now();
  }

  /** Switching scope reloads the fields from that scope, so occurrence values never land on the series. */
  chooseScope(next: Scope): void {
    const event = this.event();
    if (!event || next === this.scope()) return;
    this.scope.set(next);
    this.fill(event, next);
  }

  onSubmit(e: Event): void {
    e.preventDefault();
    this.save();
  }

  save(): void {
    if (this.saving()) return;
    const problem = this.problem();
    if (problem) {
      this.error.set(problem);
      return;
    }
    this.saving.set(true);
    this.error.set(null);
    this.request().subscribe({
      next: () => {
        this.saving.set(false);
        this.saved.emit();
      },
      error: (err: ApiError) => {
        this.saving.set(false);
        this.error.set(errorKey(err));
      },
    });
  }

  close(): void {
    if (!this.saving()) this.closed.emit();
  }

  onBackdrop(e: MouseEvent): void {
    if (e.target === e.currentTarget) this.close();
  }

  private problem(): string | null {
    if (!this.title().trim()) return 'calendar.event.errors.title';
    if (this.calendarId() === '') return 'calendar.event.errors.calendar';
    if (!ISO_DATE.test(this.date())) return 'calendar.event.errors.date';
    if (!this.allDay()) {
      if (!CLOCK.test(this.startTime()) || !CLOCK.test(this.endTime())) return 'calendar.event.errors.time';
      if (this.spanDays === 0 && this.endTime() === this.startTime()) return 'calendar.event.errors.end_before_start';
    }
    if (!this.occurrenceMode() && this.customRule() === null && repeatProblem(this.repeatState(), this.date())) {
      return 'calendar.event.errors.repeat';
    }
    return null;
  }

  private request(): Observable<unknown> {
    const event = this.event();
    const occurrence = this.occurrence();
    const title = this.title().trim();
    const location = this.location().trim() || null;
    const startDate = this.date();
    const startTime = this.allDay() ? null : this.startTime();
    const endTime = this.allDay() ? null : this.endTime();
    const endDate = endDateFor(startDate, this.spanDays, startTime, endTime);

    if (event && occurrence !== null && this.occurrenceMode()) {
      const body: OccurrenceRequest = {
        title,
        description: this.description,
        location,
        start_date: startDate,
        start_time: startTime,
        end_date: endDate,
        end_time: endTime,
      };
      return this.calendar.editOccurrence(event.id, occurrence, body, this.version);
    }

    const body: EventRequest = {
      title,
      description: this.description,
      location,
      all_day: this.allDay(),
      time_zone: this.timeZone,
      start_date: startDate,
      start_time: startTime,
      end_date: endDate,
      end_time: endTime,
      recurrence_rule: this.ruleToSave(),
      member_ids: this.memberIds(),
      calendar_id: this.calendarId(),
      reminders: this.reminderList(),
    };
    return event ? this.calendar.updateEvent(event.id, body, this.version) : this.calendar.createEvent(body);
  }

  /** Loads the form from the event for one scope. Occurrence values come from its exception, if it has one. */
  private fill(event: EventDetail, scope: Scope): void {
    const occurrence = this.occurrence();
    const occurrenceMode = scope === 'occurrence' && occurrence !== null;
    const exception: EventException | undefined = occurrenceMode
      ? event.exceptions.find((e) => e.kind === 'edit' && e.occurrence_date === occurrence)
      : undefined;

    this.title.set(exception?.title ?? event.title);
    this.description = exception?.description ?? event.description;
    this.location.set(exception?.location ?? event.location ?? '');
    this.allDay.set(event.all_day);
    this.date.set(occurrenceMode ? (exception?.start_date ?? occurrence) : event.start_date);
    this.startTime.set(exception?.start_time ?? event.start_time ?? DEFAULT_START);
    this.endTime.set(exception?.end_time ?? event.end_time ?? DEFAULT_END);
    this.spanDays =
      exception?.start_date && exception.end_date
        ? daysBetween(exception.start_date, exception.end_date)
        : daysBetween(event.start_date, event.end_date);
    this.memberIds.set([...event.member_ids]);
    this.loadReminder(event.reminders);
    this.loadRepeat(event);
  }

  /** The form holds one reminder. A stored event with more keeps only the first, until the user picks another. */
  private loadReminder(saved: number[]): void {
    const minutes = saved[0] ?? null;
    const custom = minutes !== null && !REMINDER_PRESETS.includes(minutes);
    this.reminder.set(minutes);
    this.customActive.set(custom);
    if (minutes !== null && custom) {
      const unit: ReminderUnit = minutes % 1440 === 0 ? 'days' : minutes % 60 === 0 ? 'hours' : 'minutes';
      this.customUnit.set(unit);
      this.customAmount.set(String(minutes / UNIT_MINUTES[unit]));
    } else {
      this.customAmount.set('');
    }
  }

  /** Loads the repeat builder from the series rule. A rule the builder cannot rebuild is kept as it is. */
  private loadRepeat(event: EventDetail): void {
    const rule = event.recurrence_rule;
    const state = rule ? stateFromRule(rule, event.start_date) : null;
    this.repeatState.set(state ?? defaultRepeat(event.start_date));
    this.customRule.set(rule && !state ? rule : null);
    this.anchor.set(event.start_date);
  }

  private ruleToSave(): string | null {
    return this.customRule() ?? buildRule(this.repeatState(), this.date());
  }

  private anchorOn(date: string): void {
    if (ISO_DATE.test(date)) this.anchor.set(date);
  }
}
