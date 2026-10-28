import { Component, DestroyRef, OnInit, computed, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable, catchError, from, of, switchMap } from 'rxjs';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { SkeletonComponent } from '../../shared/ui/skeleton/skeleton.component';
import { WallScrollCueDirective } from '../wall/display/wall-scroll-cue.directive';
import { currentDeviceLocation } from './device-location';
import { WeatherIconComponent } from './weather-icon.component';
import {
  TemperatureUnit,
  WeatherAnimation,
  WeatherCondition,
  WeatherForecast,
  WeatherResponse,
} from './weather.models';
import { WeatherService } from './weather.service';
import { TimeFormatService } from '../../core/time-format/time-format.service';

export type WeatherTileVariant = 'full' | 'compact';
type TileState = 'loading' | 'ready' | 'unconfigured' | 'error';
export type WeatherScene = 'sun' | 'cloud' | 'drizzle' | 'rain' | 'storm' | 'snow' | 'fog' | 'none';
/** The time of day sets the sky tint: dawn and dusk warm it, night turns it dark. */
export type DayPart = 'dawn' | 'day' | 'dusk' | 'night';

const CALM_PARTICLE_COUNT = 14;
const LIVE_PARTICLE_COUNT = 30;
/** The hour strip scrolls sideways, so the wide card can keep the full 12 hours in one row. */
const HOUR_COUNT = 12;

/** Base fall or fly time, in seconds, per scene. Lower is faster. Live motion scales it down. */
const SCENE_PARTICLE_SECONDS: Partial<Record<WeatherScene, number>> = {
  rain: 1.2,
  drizzle: 2.2,
  storm: 0.8,
  snow: 7,
};

export function dayPartFor(hour: number): DayPart {
  if (hour >= 5 && hour < 8) return 'dawn';
  if (hour >= 8 && hour < 17) return 'day';
  if (hour >= 17 && hour < 20) return 'dusk';
  return 'night';
}

function sceneFor(condition: WeatherCondition | undefined): WeatherScene {
  switch (condition) {
    case 'clear':
      return 'sun';
    case 'partly_cloudy':
    case 'cloudy':
      return 'cloud';
    case 'drizzle':
      return 'drizzle';
    case 'rain':
      return 'rain';
    case 'storm':
      return 'storm';
    case 'snow':
      return 'snow';
    case 'fog':
      return 'fog';
    default:
      return 'none';
  }
}

/**
 * Family weather card. Loads its own forecast. `full` matches the kid Today mock
 * (today and tomorrow); `compact` is a single row for the parent dashboard.
 * Parents see a link to set up the location when it is missing; others see a neutral hint.
 */
@Component({
  selector: 'app-weather-tile',
  standalone: true,
  imports: [TranslocoModule, RouterLink, LoadingComponent, SkeletonComponent, WeatherIconComponent, WallScrollCueDirective],
  templateUrl: './weather-tile.component.html',
  styleUrl: './weather-tile.component.scss',
  host: { class: 'weather-tile' },
})
export class WeatherTileComponent implements OnInit {
  private weather = inject(WeatherService);
  private destroyRef = inject(DestroyRef);
  private timeFormat = inject(TimeFormatService);

  readonly variant = input<WeatherTileVariant>('full');
  readonly canConfigure = input(false);

  readonly state = signal<TileState>('loading');
  readonly refreshing = signal(false);
  readonly data = signal<WeatherResponse | null>(null);
  readonly dayPart = signal<DayPart>(dayPartFor(new Date().getHours()));

  readonly forecast = computed<WeatherForecast | null>(() => this.data()?.forecast ?? null);
  readonly shownHours = computed(() => (this.forecast()?.hourly ?? []).slice(0, HOUR_COUNT));
  readonly unit = computed<TemperatureUnit>(() => this.data()?.temperature_unit ?? 'c');
  readonly unitLetter = computed(() => (this.unit() === 'f' ? 'F' : 'C'));
  readonly location = computed(() => this.data()?.location_name ?? null);
  readonly scene = computed<WeatherScene>(() => sceneFor(this.forecast()?.current.condition));
  readonly animation = computed<WeatherAnimation>(() => this.data()?.animation ?? 'calm');
  readonly particles = computed(() => {
    if (this.scene() === 'sun' || this.scene() === 'cloud') {
      return [];
    }
    return Array.from({ length: this.animation() === 'live' ? LIVE_PARTICLE_COUNT : CALM_PARTICLE_COUNT }, (_, i) => i);
  });

  ngOnInit(): void {
    this.load();
  }

  /** Inline style for one particle: position, timing, length, size and sway. Deterministic per index. */
  particleStyle(p: number): string {
    const jitter = ((p * 37) % 100) / 100;
    const scale = SCENE_PARTICLE_SECONDS[this.scene()] ?? 1;
    const base = (0.9 + jitter * 0.8) * scale;
    const delay = (p * 0.37) % Math.max(base, 0.1);
    return [
      `left: ${(p * 37) % 100}%`,
      `animation-delay: -${delay.toFixed(2)}s`,
      `--base: ${base.toFixed(2)}s`,
      `--len: ${Math.round(10 + jitter * 12)}px`,
      `--size: ${(3 + jitter * 4).toFixed(1)}px`,
      `--sway: ${Math.round((jitter - 0.5) * 24)}px`,
    ].join('; ');
  }

  load(refresh = false): void {
    if (!refresh) this.state.set('loading');
    this.refreshing.set(refresh);
    this.dayPart.set(dayPartFor(new Date().getHours()));
    this.weather
      .forecast(undefined, refresh)
      .pipe(
        switchMap((family) => (family.configured ? of(family) : this.deviceForecast(family, refresh))),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (res) => {
          this.refreshing.set(false);
          this.data.set(res);
          if (!res.configured) {
            this.state.set('unconfigured');
          } else if (res.forecast) {
            this.state.set('ready');
          } else {
            this.state.set('error');
          }
        },
        error: () => {
          this.refreshing.set(false);
          // A failed refresh keeps the forecast on screen; the next refresh can try again.
          if (refresh && this.data()) return;
          this.data.set(null);
          this.state.set('error');
        },
      });
  }

  refresh(): void {
    this.load(true);
  }

  /**
   * The family has no location, so ask the browser for the viewer's position. If the browser
   * gives none (denied, unsupported, timed out), keep the family's unconfigured response.
   */
  private deviceForecast(family: WeatherResponse, refresh: boolean): Observable<WeatherResponse> {
    return from(currentDeviceLocation()).pipe(
      catchError(() => of(null)),
      switchMap((coords) => (coords ? this.weather.forecast(coords, refresh) : of(family))),
    );
  }

  round(value: number): number {
    return Math.round(value);
  }

  /** Hour of day in the viewer's locale, such as "3 PM" in English. */
  hourLabel(iso: string): string {
    return new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ hour: 'numeric' })).format(new Date(iso));
  }
}
