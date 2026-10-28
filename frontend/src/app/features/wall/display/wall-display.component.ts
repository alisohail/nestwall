import {
  Component,
  HostListener,
  OnDestroy,
  OnInit,
  computed,
  inject,
  signal,
} from "@angular/core";
import { Router } from "@angular/router";
import { TranslocoModule, TranslocoService } from "@ngneat/transloco";
import { ApiError } from "../../../core/api/api-client.service";
import { clockOptions, setTwelveHour } from "../../../core/time-format/clock-style";
import { LoadingComponent } from "../../../shared/ui/loading/loading.component";
import { WeatherIconComponent } from "../../weather/weather-icon.component";
import { currentDeviceLocation } from "../../weather/device-location";
import { WeatherCondition, WeatherDay } from "../../weather/weather.models";
import { WallLiveUpdates } from "../live/wall-live-updates";
import * as format from "./wall-display.format";
import { WallDisplayService } from "./wall-display.service";
import {
  WallSummary,
  WallSummaryAttendee,
  WallSummaryEvent,
  WallSummaryTask,
} from "./wall-summary.models";
import { WallScrollCueDirective } from "./wall-scroll-cue.directive";

const CLOCK_MS = 20_000;
const SUB_MS = 7_000;
const FADE_MS = 650;
const SHAKE_MS = 500;
const EXIT_HOLD_MS = 2_500;
const EXIT_CODE_MIN = 4;
const EXIT_CODE_MAX = 6;
const KEY_DELETE = "\u232B";
const KEY_SUBMIT = "\u2713";
const PAD_KEYS = ["1", "2", "3", "4", "5", "6", "7", "8", "9", KEY_DELETE, "0", KEY_SUBMIT];
const CODE_SLOTS = [0, 1, 2, 3, 4, 5];
const DONE_SHOWN = 3;
const WET: WeatherCondition[] = ["drizzle", "rain", "snow", "storm"];
const CONFETTI = Array.from({ length: 22 }, (_, i) => ({
  left: (i * 4.6) % 100,
  colour: ["#fcd34d", "#f472b6", "#5eead4", "#a5b4fc"][i % 4],
  duration: 3 + (i % 5) * 0.7,
  delay: -((i % 7) * 0.6),
}));

interface WeatherDayView {
  condition: WeatherCondition;
  label: string;
  high: number;
  low: number;
  precip: number | null;
  wet: boolean;
  unit: string;
}

interface WeatherTodayView extends WeatherDayView {
  temp: number;
  feels: number;
}

interface PendingRow {
  id: string;
  title: string;
  due: string;
  late: boolean;
}

interface AvatarView {
  id: string;
  initial: string;
  colour: string;
  /** Picture URL, or null when the member has no picture the wall may show. */
  avatar: string | null;
}

interface BonusCardView {
  id: string;
  title: string;
  points: number;
  meta: string;
}

interface KidView {
  id: string;
  name: string;
  initial: string;
  colour: string;
  avatar: string | null;
  pct: number;
  countText: string;
  pending: PendingRow[];
  done: { id: string; title: string; time: string }[];
  doneCount: number;
  moreDone: number;
  allDone: boolean;
  hasTasks: boolean;
}

interface AgendaRow {
  key: string;
  kind: "head" | "event" | "empty";
  label?: string;
  sub?: string;
  time?: string;
  title?: string;
  location?: string | null;
  inText?: string;
  past?: boolean;
  next?: boolean;
  tomorrow?: boolean;
  attendees?: AvatarView[];
  everyone?: boolean;
}

@Component({
    selector: "app-wall-display",
    imports: [
        TranslocoModule,
        LoadingComponent,
        WeatherIconComponent,
        WallScrollCueDirective,
    ],
    templateUrl: "./wall-display.component.html",
    styleUrl: "./wall-display.component.scss"
})
export class WallDisplayComponent implements OnInit, OnDestroy {
  private display = inject(WallDisplayService);
  private transloco = inject(TranslocoService);
  private router = inject(Router);
  private live = new WallLiveUpdates(() => this.load());

  readonly confetti = CONFETTI;
  readonly summary = signal<WallSummary | null>(null);
  readonly offline = signal(false);
  readonly lastUpdated = signal<Date | null>(null);
  readonly now = signal(new Date());
  readonly subIndex = signal(0);
  readonly subFading = signal(false);
  readonly keypadOpen = signal(false);
  readonly keypadUnlocked = signal(false);
  readonly keypadCode = signal("");
  readonly keypadMsg = signal("");
  readonly keypadError = signal(false);
  readonly keypadBusy = signal(false);
  readonly keypadShake = signal(false);
  readonly padKeys = PAD_KEYS;
  readonly codeSlots = CODE_SLOTS;

  private loading = false;
  private locationAsked = false;
  private deviceCoords: { lat: number; lon: number } | undefined;
  private clockTimer?: ReturnType<typeof setInterval>;
  private subTimer?: ReturnType<typeof setInterval>;
  private fadeTimer?: ReturnType<typeof setTimeout>;
  private holdTimer?: ReturnType<typeof setTimeout>;
  private shakeTimer?: ReturnType<typeof setTimeout>;
  private wakeLock: WakeLockSentinel | null = null;

  readonly zone = computed(() => {
    const s = this.summary();
    return s ? format.usableZone(s.timezone) : undefined;
  });

  readonly night = computed(() => format.isNightTime(this.now(), this.zone()));

  readonly locale = computed(() => this.transloco.getActiveLang());

  readonly clock = computed(() => {
    const parts = new Intl.DateTimeFormat(this.locale(), clockOptions({
      timeZone: this.zone(),
      hour: "numeric",
      minute: "2-digit",
    })).formatToParts(this.now());
    const part = (type: string) =>
      parts.find((p) => p.type === type)?.value ?? "";
    return {
      hour: part("hour"),
      minute: part("minute"),
      period: part("dayPeriod"),
    };
  });

  readonly dateLine = computed(() =>
    new Intl.DateTimeFormat(this.locale(), {
      timeZone: this.zone(),
      weekday: "long",
      day: "numeric",
      month: "long",
    }).format(this.now()),
  );

  readonly greeting = computed(() => {
    const s = this.summary();
    if (!s) return "";
    const period = format.greetingPeriod(this.now(), this.zone());
    return this.transloco.translate(`wall.display.greeting.${period}`, {
      family: s.family_name,
    });
  });

  private readonly forecast = computed(() => {
    const s = this.summary();
    return s?.options.show_weather && s.weather?.configured
      ? s.weather.forecast
      : null;
  });

  readonly tempUnit = computed(() =>
    this.summary()?.weather?.temperature_unit === "f" ? "°F" : "°C",
  );

  readonly weatherToday = computed<WeatherTodayView | null>(() => {
    const forecast = this.forecast();
    if (!forecast) return null;
    return {
      ...this.dayView(forecast.today, this.tempUnit()),
      temp: Math.round(forecast.current.temperature),
      feels: Math.round(forecast.current.feels_like),
    };
  });

  readonly weatherTomorrow = computed<WeatherDayView | null>(() => {
    const forecast = this.forecast();
    return forecast && this.summary()?.options.show_tomorrow
      ? this.dayView(forecast.tomorrow, this.tempUnit())
      : null;
  });

  readonly kids = computed<KidView[]>(() => {
    const s = this.summary();
    if (!s) return [];
    return s.children.map((child) => this.kidView(child, s));
  });

  readonly agenda = computed<AgendaRow[]>(() => {
    const s = this.summary();
    if (!s) return [];
    const zone = this.zone();
    const locale = this.locale();
    const now = this.now();
    const tomorrowKey = format.addDays(s.date, 1);
    const sorted = [...s.events].sort((a, b) =>
      a.all_day === b.all_day
        ? Date.parse(a.starts_at) - Date.parse(b.starts_at)
        : a.all_day
          ? -1
          : 1,
    );
    const today = sorted.filter((e) => e.date === s.date);
    const tomorrow = sorted.filter((e) => e.date === tomorrowKey);
    const later = sorted.filter((e) => e.date > tomorrowKey);

    const rows: AgendaRow[] = [
      {
        key: "h-today",
        kind: "head",
        label: this.transloco.translate("wall.display.day_today"),
        sub: format.formatDayKey(s.date, locale),
      },
    ];
    if (!today.length)
      rows.push({
        key: "empty-today",
        kind: "empty",
        title: this.transloco.translate("wall.display.no_events_today"),
      });
    const nextEvent = [...today, ...tomorrow].find(
      (e) => !e.all_day && Date.parse(e.starts_at) > now.getTime(),
    );
    today.forEach((e, i) => {
      const ended = !e.all_day && Date.parse(e.ends_at) <= now.getTime();
      const next = e === nextEvent;
      rows.push({
        ...this.eventRow(e, `t-${i}`, zone, locale),
        past: ended,
        next,
        inText: next ? this.untilText(e, now) : undefined,
      });
    });
    if (tomorrow.length) {
      rows.push({
        key: "h-tomorrow",
        kind: "head",
        tomorrow: true,
        label: this.transloco.translate("wall.display.day_tomorrow"),
        sub: format.formatDayKey(tomorrowKey, locale),
      });
      tomorrow.forEach((e, i) => {
        const next = e === nextEvent;
        rows.push({
          ...this.eventRow(e, `m-${i}`, zone, locale),
          tomorrow: true,
          next,
          inText: next ? this.untilText(e, now) : undefined,
        });
      });
    }
    if (later.length) {
      rows.push({
        key: "h-later",
        kind: "head",
        tomorrow: true,
        label: this.transloco.translate("wall.display.day_later"),
      });
      later.forEach((e, i) =>
        rows.push({
          ...this.eventRow(e, `l-${i}`, zone, locale, e.date),
          tomorrow: true,
        }),
      );
    }
    return rows;
  });

  readonly bonus = computed<BonusCardView[]>(() => {
    const s = this.summary();
    if (!s?.options.show_bonus_banner) return [];
    const zone = this.zone();
    const locale = this.locale();
    return s.bonus_tasks.map((task) => ({
      id: task.id,
      title: task.title,
      points: task.points,
      meta: this.transloco.translate("wall.display.bonus_meta", {
        mode: this.transloco.translate(
          `wall.display.bonus_mode.${task.claim_mode}`,
        ),
        time: format.formatClock(task.ends_at, zone, locale),
      }),
    }));
  });

  readonly messages = computed<string[]>(() => {
    const s = this.summary();
    if (!s) return [];
    const t = (key: string, params?: Record<string, unknown>) =>
      this.transloco.translate(key, params);
    const zone = this.zone();
    const locale = this.locale();
    const now = this.now();
    const pending = s.children.reduce(
      (sum, c) => sum + c.tasks.filter((task) => task.state !== "done").length,
      0,
    );
    const eventsLeft = s.events.filter(
      (e) =>
        e.date === s.date &&
        !e.all_day &&
        Date.parse(e.ends_at) > now.getTime(),
    ).length;
    const messages = [
      t("wall.display.sub_day", {
        date: this.dateLine(),
        tasks: pending,
        events: eventsLeft,
      }),
    ];

    const weather = this.weatherTomorrow();
    if (weather) {
      messages.push(
        t("wall.display.sub_weather", {
          condition: weather.label,
          high: weather.high,
        }),
      );
    }
    s.children
      .filter(
        (c) =>
          c.tasks.length > 0 && c.tasks.every((task) => task.state === "done"),
      )
      .forEach((c) =>
        messages.push(t("wall.display.sub_done", { name: c.name })),
      );

    const tomorrowKey = format.addDays(s.date, 1);
    const next = s.events
      .filter((e) => e.date === tomorrowKey)
      .sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at))[0];
    if (next) {
      messages.push(
        next.all_day
          ? t("wall.display.sub_tomorrow_all_day", { title: this.eventTitle(next) })
          : t("wall.display.sub_tomorrow_event", {
              title: this.eventTitle(next),
              time: format.formatClock(next.starts_at, zone, locale),
            }),
      );
    }
    return messages;
  });

  readonly subMessage = computed(() => {
    const list = this.messages();
    return list.length ? list[this.subIndex() % list.length] : "";
  });

  readonly statusText = computed(() => {
    const at = this.lastUpdated();
    if (!at) return "";
    const time = format.formatClock(at, this.zone(), this.locale());
    return this.transloco.translate(
      this.offline()
        ? "wall.display.status_offline"
        : "wall.display.status_live",
      { time },
    );
  });

  ngOnInit(): void {
    this.offline.set(!navigator.onLine);
    this.load();
    this.live.start();
    this.requestWakeLock();
    this.clockTimer = setInterval(() => this.now.set(new Date()), CLOCK_MS);
    this.subTimer = setInterval(() => this.nextSubMessage(), SUB_MS);
  }

  ngOnDestroy(): void {
    this.live.stop();
    clearInterval(this.clockTimer);
    clearInterval(this.subTimer);
    clearTimeout(this.fadeTimer);
    clearTimeout(this.holdTimer);
    clearTimeout(this.shakeTimer);
    this.wakeLock?.release().catch(() => undefined);
    this.wakeLock = null;
  }

  @HostListener("window:online")
  onOnline(): void {
    this.load();
  }

  @HostListener("window:offline")
  onOffline(): void {
    this.offline.set(true);
  }

  @HostListener("document:visibilitychange")
  onVisibilityChange(): void {
    if (document.visibilityState === "visible") {
      this.requestWakeLock();
      this.load();
    }
  }

  /** Holding the bottom-left corner for 2.5 s opens the keypad. Lifting the finger early cancels it. */
  startExitHold(): void {
    this.cancelExitHold();
    this.holdTimer = setTimeout(() => this.openKeypad(), EXIT_HOLD_MS);
  }

  cancelExitHold(): void {
    clearTimeout(this.holdTimer);
  }

  openKeypad(): void {
    this.cancelExitHold();
    this.keypadOpen.set(true);
  }

  closeKeypad(): void {
    this.keypadOpen.set(false);
    this.keypadUnlocked.set(false);
    this.keypadCode.set("");
    this.keypadMsg.set("");
    this.keypadError.set(false);
    this.keypadBusy.set(false);
  }

  pressKey(key: string): void {
    if (this.keypadBusy() || this.keypadUnlocked()) return;
    this.keypadError.set(false);
    if (key === KEY_DELETE) {
      this.keypadCode.update((code) => code.slice(0, -1));
    } else if (key === KEY_SUBMIT) {
      this.submitCode();
    } else if (this.keypadCode().length < EXIT_CODE_MAX) {
      this.keypadCode.update((code) => code + key);
    }
  }

  keyDisabled(key: string): boolean {
    if (this.keypadBusy()) return true;
    return key === KEY_SUBMIT && this.keypadCode().length < EXIT_CODE_MIN;
  }

  keyLabel(key: string): string | null {
    if (key === KEY_DELETE) return this.transloco.translate("wall.display.exit.delete");
    if (key === KEY_SUBMIT) return this.transloco.translate("wall.display.exit.submit");
    return null;
  }

  exitWall(): void {
    this.leave("/login");
  }

  unpairDisplay(): void {
    this.keypadBusy.set(true);
    this.display.unpair(this.keypadCode()).subscribe({
      next: () => this.leave("/pair"),
      error: (error: ApiError) => {
        this.keypadUnlocked.set(false);
        this.refuseCode(error);
      },
    });
  }

  stayOnWall(): void {
    this.closeKeypad();
  }

  private submitCode(): void {
    const code = this.keypadCode();
    if (code.length < EXIT_CODE_MIN) return;
    this.keypadBusy.set(true);
    this.display.verifyExitCode(code).subscribe({
      next: () => {
        this.keypadBusy.set(false);
        this.keypadUnlocked.set(true);
        this.keypadMsg.set("");
      },
      error: (error: ApiError) => this.refuseCode(error),
    });
  }

  /** Clears the typed code and shows why the server refused it. */
  private refuseCode(error: ApiError): void {
    this.keypadBusy.set(false);
    if (error.status === 401) {
      this.leave("/pair");
      return;
    }
    this.keypadCode.set("");
    this.keypadMsg.set(this.exitErrorMessage(error));
    this.keypadError.set(true);
    this.keypadShake.set(true);
    clearTimeout(this.shakeTimer);
    this.shakeTimer = setTimeout(() => this.keypadShake.set(false), SHAKE_MS);
  }

  private exitErrorMessage(error: ApiError): string {
    const params = error.params ?? {};
    if (error.code === "exit_locked") {
      const seconds = Number(params["retry_after"]) || 0;
      const minutes = Math.max(1, Math.ceil(seconds / 60));
      return this.transloco.translate("wall.display.exit.locked", { minutes });
    }
    if (error.code === "exit_code_wrong") {
      const left = Number(params["attempts_left"]) || 0;
      return left === 1
        ? this.transloco.translate("wall.display.exit.wrong_code_last")
        : this.transloco.translate("wall.display.exit.wrong_code", { count: left });
    }
    return this.transloco.translate("wall.display.exit.error");
  }

  private leave(path: string): void {
    this.closeKeypad();
    this.router.navigateByUrl(path);
  }

  private load(): void {
    if (this.loading) return;
    this.loading = true;
    this.display.summary(this.deviceCoords).subscribe({
      next: (summary) => {
        this.loading = false;
        setTwelveHour(summary.time_format !== '24h');
        this.summary.set(summary);
        this.offline.set(false);
        this.lastUpdated.set(new Date());
        this.askDeviceLocation(summary);
      },
      error: (error: ApiError) => {
        this.loading = false;
        if (error.status === 401) {
          this.router.navigateByUrl("/pair");
          return;
        }
        this.offline.set(true);
      },
    });
  }

  /**
   * The family has no weather location, so ask the display's browser once and
   * reload with its coordinates. A refusal leaves the weather tiles hidden.
   */
  private askDeviceLocation(s: WallSummary): void {
    if (this.locationAsked || !s.options.show_weather || s.weather?.configured)
      return;
    this.locationAsked = true;
    currentDeviceLocation().then(
      (coords) => {
        this.deviceCoords = { lat: coords.latitude, lon: coords.longitude };
        this.load();
      },
      () => undefined,
    );
  }

  private nextSubMessage(): void {
    const count = this.messages().length;
    if (count < 2) return;
    this.subFading.set(true);
    this.fadeTimer = setTimeout(() => {
      this.subIndex.update((i) => (i + 1) % count);
      this.subFading.set(false);
    }, FADE_MS);
  }

  private async requestWakeLock(): Promise<void> {
    if (!("wakeLock" in navigator) || this.wakeLock) return;
    try {
      this.wakeLock = await navigator.wakeLock.request("screen");
      this.wakeLock.addEventListener("release", () => (this.wakeLock = null));
    } catch {
      this.wakeLock = null;
    }
  }

  private kidView(
    child: WallSummary["children"][number],
    s: WallSummary,
  ): KidView {
    const zone = format.usableZone(s.timezone);
    const locale = this.locale();
    const pendingTasks = child.tasks.filter((t) => t.state !== "done");
    const doneTasks = child.tasks
      .filter((t) => t.state === "done")
      .sort(
        (a, b) =>
          Date.parse(b.completed_at ?? b.due_at) -
          Date.parse(a.completed_at ?? a.due_at),
      );
    const total = child.tasks.length;
    const done = doneTasks.length;
    const pct = total ? Math.round((done / total) * 100) : 0;
    const allDone = total > 0 && pendingTasks.length === 0;

    const pending = pendingTasks.map((task) => ({
      id: task.occurrence_id,
      title: task.title,
      due: format.formatDue(task.due_at, s.date, zone, locale),
      late: task.state === "overdue",
    }));

    let countText = this.transloco.translate("wall.display.count_no_tasks");
    if (allDone)
      countText = this.transloco.translate("wall.display.count_all_done");
    else if (total)
      countText = this.transloco.translate("wall.display.count_done", {
        done,
        total,
      });
    if (total && child.points !== null && this.summary()?.options.show_points) {
      countText = this.transloco.translate("wall.display.count_points", {
        count: countText,
        points: child.points,
      });
    }

    return {
      id: child.member_id,
      name: child.name,
      initial: child.name.charAt(0).toUpperCase(),
      colour: child.colour,
      avatar: child.avatar_id
        ? this.display.avatarUrl(child.avatar_id, 256)
        : null,
      pct,
      countText,
      pending,
      done: doneTasks
        .slice(0, DONE_SHOWN)
        .map((task) => this.doneRow(task, zone, locale)),
      doneCount: done,
      moreDone: Math.max(0, done - DONE_SHOWN),
      allDone,
      hasTasks: total > 0,
    };
  }

  private doneRow(
    task: WallSummaryTask,
    zone: string | undefined,
    locale: string,
  ) {
    return {
      id: task.occurrence_id,
      title: task.title,
      time: format.formatClock(task.completed_at ?? task.due_at, zone, locale),
    };
  }

  private eventRow(
    e: WallSummaryEvent,
    key: string,
    zone: string | undefined,
    locale: string,
    dayKey?: string,
  ): AgendaRow {
    let time: string;
    if (dayKey) time = format.formatDayKey(dayKey, locale);
    else
      time = e.all_day
        ? this.transloco.translate("wall.display.all_day")
        : format.formatClock(e.starts_at, zone, locale);
    const attendees = e.attendees.map((a) => this.avatarView(a));
    return {
      key,
      kind: "event",
      time,
      title: this.eventTitle(e),
      location: e.location,
      attendees,
      everyone: attendees.length === 0,
    };
  }

  private eventTitle(e: WallSummaryEvent): string {
    return e.kind === "birthday" ? this.birthdayTitle(e.title, e.age) : e.title;
  }

  private birthdayTitle(name: string, age?: number): string {
    return age
      ? this.transloco.translate("wall.display.birthday_age", { name, age })
      : this.transloco.translate("wall.display.birthday", { name });
  }

  private avatarView(a: WallSummaryAttendee): AvatarView {
    return {
      id: a.member_id,
      initial: a.name.charAt(0).toUpperCase(),
      colour: a.colour,
      avatar: a.avatar_id ? this.display.avatarUrl(a.avatar_id, 96) : null,
    };
  }

  private untilText(e: WallSummaryEvent, now: Date): string {
    const minutes = format.minutesUntil(e.starts_at, now);
    if (minutes >= 60) {
      return this.transloco.translate("wall.display.in_hours", {
        hours: Math.floor(minutes / 60),
        minutes: minutes % 60,
      });
    }
    return this.transloco.translate("wall.display.in_minutes", { minutes });
  }

  private dayView(day: WeatherDay, unit: string): WeatherDayView {
    return {
      condition: day.condition,
      label: this.transloco.translate(`weather.condition.${day.condition}`),
      high: Math.round(day.high),
      low: Math.round(day.low),
      precip: day.precip_chance,
      wet: WET.includes(day.condition),
      unit,
    };
  }
}
