import { Component, DestroyRef, HostListener, OnChanges, OnInit, SimpleChanges, WritableSignal, computed, inject, input, signal } from '@angular/core';
import { NgTemplateOutlet } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { Subject, catchError, map, of, switchMap } from 'rxjs';
import { CalendarService } from '../calendar.service';
import { CalendarItem } from '../calendar.models';
import { clockIn, countdown, shortDay } from '../calendar.dates';
import { DisplayZoneService } from '../../../core/timezone/display-zone.service';
import type { MonthPill } from '../calendar.component';
import { EventDetailComponent } from '../event-detail/event-detail.component';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { SkeletonComponent } from '../../../shared/ui/skeleton/skeleton.component';
import { addDaysIso, formatDay, todayIso } from '../../tasks/tasks.format';

export interface BandRow {
  item: CalendarItem;
  /** Clock time in the family zone. Empty for all-day items. */
  time: string;
  colour: string;
  /** Names joined, for the tooltip and screen readers. Empty when the event is for everyone. */
  people: string;
  /** The people on the event, shown as avatars. Empty when the event is for everyone. */
  members: MemberDetail[];
  past: boolean;
  next: boolean;
  countdown: { key: string; params: Record<string, number> } | null;
}

/** Rows shown in each list before "More". */
const LIST_LIMIT = 5;
const LOOKAHEAD_DAYS = 7;
const TICK_MS = 30_000;

type ListKey = 'today' | 'coming';
type FilterField = 'person' | 'calendar';
interface CalendarOption {
  id: string;
  name: string;
}
/** A card's filter. An empty list means everyone or all calendars. Otherwise an event passes if it matches any picked id. */
interface BandFilter {
  person: string[];
  calendar: string[];
}

/** Today and coming up, from the calendar range. Events only; tasks stay on the Tasks pages. */
@Component({
    selector: 'app-agenda-band',
    imports: [TranslocoModule, NgTemplateOutlet, AvatarComponent, IconComponent, EventDetailComponent, SkeletonComponent],
    templateUrl: './agenda-band.component.html',
    styleUrl: './agenda-band.component.scss'
})
export class AgendaBandComponent implements OnInit, OnChanges {
  private calendar = inject(CalendarService);
  private displayZone = inject(DisplayZoneService);
  private family = inject(FamilyService);
  private destroyRef = inject(DestroyRef);

  /** IANA zone of the family. Falls back to the viewer's zone when unset. */
  zone = input<string | undefined>(undefined);
  /** Bump this to reload the band, for example after an event is saved. */
  refresh = input<number>(0);

  protected readonly formatDay = formatDay;
  protected readonly shortDay = shortDay;

  readonly loading = signal(true);
  readonly failed = signal(false);
  readonly now = signal(Date.now());
  /** The event open in the detail popup, with the people shown on it. */
  readonly selected = signal<{ pill: MonthPill; people: MemberDetail[] } | null>(null);
  /** The full-list view that "More" opened, if any. */
  readonly more = signal<'today' | 'coming' | null>(null);
  private readonly items = signal<CalendarItem[]>([]);
  private readonly members = signal<Map<string, MemberDetail>>(new Map());
  readonly today = computed(() => todayIso(new Date(this.now()), this.zone()));

  /** The filter whose options are showing, if any. Only one is open at a time. */
  readonly openFilter = signal<{ list: ListKey; field: FilterField } | null>(null);
  private readonly cardFilters: Record<ListKey, Record<FilterField, WritableSignal<string[]>>> = {
    today: { person: signal<string[]>([]), calendar: signal<string[]>([]) },
    coming: { person: signal<string[]>([]), calendar: signal<string[]>([]) },
  };

  /** Family members for the person filter. */
  readonly personOptions = computed(() =>
    [...this.members().values()].sort((a, b) => a.display_name.localeCompare(b.display_name)),
  );

  /** Calendars with events in the range, by name. */
  readonly calendarOptions = computed<CalendarOption[]>(() => {
    const names = new Map<string, string>();
    for (const item of this.items()) {
      if (item.calendar_id) names.set(item.calendar_id, item.calendar_name ?? '');
    }
    return [...names].map(([id, name]) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name));
  });

  /** Today's events that pass the card's filters, in order, including ones that have ended. */
  readonly todayAll = computed<BandRow[]>(() => {
    const today = this.today();
    const filter = this.readFilter('today');
    const rows = this.items()
      .filter((item) => item.occurrence_date === today && this.passes(item, filter))
      .sort((a, b) => Number(b.all_day) - Number(a.all_day) || a.starts_at.localeCompare(b.starts_at));
    const nextKey = this.nextKey(rows);
    return rows.map((item) => this.toRow(item, nextKey));
  });

  /** Events in the next days that pass the card's filters, in order. */
  readonly comingAll = computed<BandRow[]>(() => {
    const today = this.today();
    const filter = this.readFilter('coming');
    return this.items()
      .filter((item) => item.occurrence_date > today && this.passes(item, filter))
      .sort((a, b) => a.starts_at.localeCompare(b.starts_at))
      .map((item) => this.toRow(item, null));
  });

  /**
   * Upcoming events always show, up to the list limit. Events that have ended fill the spare rows, most recent first,
   * and are hidden only when the list is full.
   */
  readonly todayRows = computed(() => {
    const rows = this.todayAll();
    const upcoming = rows.filter((row) => !row.past).slice(0, LIST_LIMIT);
    const past = rows.filter((row) => row.past);
    const room = LIST_LIMIT - upcoming.length;
    const shownPast = new Set(past.slice(past.length - Math.min(room, past.length)).map((row) => row.item.key));
    const shownUpcoming = new Set(upcoming.map((row) => row.item.key));
    return rows.filter((row) => (row.past ? shownPast.has(row.item.key) : shownUpcoming.has(row.item.key)));
  });
  readonly comingRows = computed(() => this.comingAll().slice(0, LIST_LIMIT));
  /** Events the card does not show. The More link counts these. */
  readonly todayHidden = computed(() => this.todayAll().length - this.todayRows().length);
  readonly comingHidden = computed(() => this.comingAll().length - this.comingRows().length);

  /** Rows of the full-list view. */
  readonly moreRows = computed(() => {
    const list = this.more();
    if (list === 'today') return this.todayAll();
    if (list === 'coming') return this.comingAll();
    return [];
  });

  private readonly load = new Subject<void>();

  ngOnInit(): void {
    this.family
      .listMembers()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.members.set(new Map(res.members.map((m) => [m.id, m]))),
        error: () => this.members.set(new Map()),
      });

    this.load
      .pipe(
        switchMap(() => {
          const from = this.today();
          return this.calendar.range(from, addDaysIso(from, LOOKAHEAD_DAYS), false).pipe(
            map((res) => ({ ok: true, items: res.items })),
            catchError(() => of({ ok: false, items: [] as CalendarItem[] })),
          );
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((res) => {
        this.items.set(res.items);
        this.failed.set(!res.ok);
        this.loading.set(false);
      });

    this.load.next();

    const timer = setInterval(() => this.now.set(Date.now()), TICK_MS);
    this.destroyRef.onDestroy(() => clearInterval(timer));
  }

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['refresh'] && !changes['refresh'].firstChange) {
      this.load.next();
    }
  }

  /** The first timed item today that has not started yet. */
  private nextKey(rows: CalendarItem[]): string | null {
    const now = this.now();
    const next = rows.find((item) => !item.all_day && Date.parse(item.starts_at) > now);
    return next ? next.key : null;
  }

  private toRow(item: CalendarItem, nextKey: string | null): BandRow {
    const now = this.now();
    const next = item.key === nextKey;
    const members = this.membersFor(item);
    return {
      item,
      time: item.all_day ? '' : clockIn(item.starts_at, this.displayZone.clockZone(this.zone())),
      colour: this.colourFor(item),
      people: members.map((m) => m.display_name).join(', '),
      members,
      past: !item.all_day && Date.parse(item.ends_at) < now,
      next,
      countdown: next ? countdown(item.starts_at, now) : null,
    };
  }

  private colourFor(item: CalendarItem): string {
    if (item.calendar_colour) return item.calendar_colour;
    const first = item.member_ids.map((id) => this.members().get(id)).find((m) => !!m);
    return first?.colour ?? 'var(--nw-color-primary)';
  }

  /** Opens the full list for today or coming up. */
  showMore(list: ListKey): void {
    this.more.set(list);
  }

  /** The filter signals for one card, read by the template. */
  filterOf(list: ListKey): Record<FilterField, WritableSignal<string[]>> {
    return this.cardFilters[list];
  }

  filterActive(list: ListKey): boolean {
    const filter = this.filterOf(list);
    return filter.person().length > 0 || filter.calendar().length > 0;
  }

  isFilterOpen(list: ListKey, field: FilterField): boolean {
    const open = this.openFilter();
    return open?.list === list && open.field === field;
  }

  /** Whether an option is picked. The "All" option (empty value) is picked when nothing else is. */
  isPicked(list: ListKey, field: FilterField, value: string): boolean {
    const picked = this.filterOf(list)[field]();
    return value === '' ? picked.length === 0 : picked.includes(value);
  }

  /** Opens one filter's options, or closes it if it is already open. */
  toggleFilter(list: ListKey, field: FilterField): void {
    this.openFilter.set(this.isFilterOpen(list, field) ? null : { list, field });
  }

  /**
   * "All" clears the filter. Any other option is added or removed, so several can be picked.
   * Removing the last one goes back to all. The options stay open while the user picks.
   */
  pickOption(list: ListKey, field: FilterField, value: string): void {
    const picked = this.filterOf(list)[field];
    if (value === '') {
      picked.set([]);
      return;
    }
    picked.update((ids) => (ids.includes(value) ? ids.filter((id) => id !== value) : [...ids, value]));
  }

  closeFilters(): void {
    this.openFilter.set(null);
  }

  @HostListener('document:click', ['$event'])
  onDocumentClick(event: Event): void {
    if (this.openFilter() && !(event.target as Element).closest('.ab-filter-bar, .ab-filter-options')) this.closeFilters();
  }

  private readFilter(list: ListKey): BandFilter {
    const filter = this.filterOf(list);
    return { person: filter.person(), calendar: filter.calendar() };
  }

  /** Events for everyone have no members, so they pass any person filter. */
  private passes(item: CalendarItem, filter: BandFilter): boolean {
    if (filter.person.length > 0 && item.member_ids.length > 0 && !item.member_ids.some((id) => filter.person.includes(id))) {
      return false;
    }
    return filter.calendar.length === 0 || (!!item.calendar_id && filter.calendar.includes(item.calendar_id));
  }

  closeMore(): void {
    this.more.set(null);
  }

  /** Escape closes an open filter first, then the full list. A popup opened from the list stays open. */
  @HostListener('document:keydown.escape')
  onEscape(): void {
    if (this.openFilter()) {
      this.closeFilters();
    } else if (this.more() && !this.selected()) {
      this.closeMore();
    }
  }

  /** Opens the event's detail in a popup on this page. The dashboard does not navigate. */
  open(row: BandRow): void {
    const item = row.item;
    const pill: MonthPill = {
      key: item.key,
      title: item.title,
      task: false,
      colour: row.colour,
      item,
      eventId: item.event_id ?? null,
      date: item.occurrence_date,
      recurring: !!item.recurring,
    };
    this.selected.set({ pill, people: row.members });
  }

  private membersFor(item: CalendarItem): MemberDetail[] {
    return item.member_ids.map((id) => this.members().get(id)).filter((m): m is MemberDetail => !!m);
  }
}
