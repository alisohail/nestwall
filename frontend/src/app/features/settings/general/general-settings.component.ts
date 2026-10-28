import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { finalize } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { FAMILY_TIMEZONES } from '../../../core/timezones';
import { FamilyService } from '../../family/family.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { currentDeviceLocation } from '../../weather/device-location';
import { TemperatureUnit, WeatherAnimation, WeatherSettings } from '../../weather/weather.models';
import { WeatherService } from '../../weather/weather.service';
import { settingsErrorKey } from '../settings.format';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';

/** Backend validation codes for the weather settings. Each has a settings.general.error.* key. */
const WEATHER_CODES = [
  'invalid_location',
  'invalid_latitude',
  'invalid_longitude',
  'invalid_location_name',
  'invalid_temperature_unit',
  'invalid_animation',
];

/** Four decimals is about 11 m, which is enough for a forecast. */
function roundCoord(value: number): number {
  return Math.round(value * 10000) / 10000;
}

/** Family-wide settings. The weather section sets the location and units for the forecast. */
@Component({
  selector: 'app-general-settings',
  standalone: true,
  imports: [FormsModule, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent],
  templateUrl: './general-settings.component.html',
  styleUrl: './general-settings.component.scss',
})
export class GeneralSettingsComponent implements UnsavedChangesAware {
  private weather = inject(WeatherService);
  private auth = inject(AuthService);
  private family = inject(FamilyService);
  private destroyRef = inject(DestroyRef);
  /** The values as last loaded or saved. Anything else counts as unsaved. */
  private baseline = '';

  readonly maxPlace = 80;

  familyName = computed(() => this.auth.currentUser()?.family?.family_name ?? '');
  canManageFamily = signal(false);
  familySaving = signal(false);
  familyError = signal<string | null>(null);
  familyDraft = '';

  /** Clock style for the whole family. Saves on click, separate from the weather form. */
  timeFormat = computed<'12h' | '24h'>(() => (this.auth.currentUser()?.family?.time_format === '24h' ? '24h' : '12h'));
  canManageTime = signal(false);
  timeSaving = signal(false);
  timeError = signal<string | null>(null);

  /** Family timezone, used for dates, due times, weather and the wall clock. Saves on change. */
  timezone = computed(() => this.auth.currentUser()?.family?.timezone ?? 'UTC');
  /** Keeps the saved zone selectable even when it is not in the shared list. */
  timezoneOptions = computed(() => {
    const zone = this.timezone();
    return FAMILY_TIMEZONES.includes(zone) ? FAMILY_TIMEZONES : [zone, ...FAMILY_TIMEZONES];
  });
  timezoneSaving = signal(false);
  timezoneError = signal<string | null>(null);

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<string | null>(null);
  loadApiError = signal<ApiError | null>(null);
  busy = signal(false);
  saved = signal(false);
  saveError = signal<string | null>(null);
  hasLocation = signal(false);
  detecting = signal(false);
  detectError = signal<string | null>(null);

  placeName = '';
  latitude: number | null = null;
  longitude: number | null = null;
  unit: TemperatureUnit = 'c';
  animation: WeatherAnimation = 'calm';

  constructor() {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.canManageFamily.set(this.auth.can('family.manage'));
        this.canManageTime.set(this.auth.can('settings.manage'));
        this.familyDraft = this.familyName();
      });
    this.load();
  }

  /** Sets the family clock style, 12h or 24h, for every page. Saves immediately. */
  setTimeFormat(format: '12h' | '24h'): void {
    if (this.timeSaving() || format === this.timeFormat()) return;
    this.timeSaving.set(true);
    this.timeError.set(null);
    this.family
      .updateTimeFormat(format)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.auth.setTimeFormat(res.time_format);
          this.timeSaving.set(false);
        },
        error: () => {
          this.timeSaving.set(false);
          this.timeError.set('settings.general.time_error');
        },
      });
  }

  /** Sets the family timezone. Saves on change. */
  setTimezone(zone: string): void {
    if (this.timezoneSaving() || zone === this.timezone()) return;
    this.timezoneSaving.set(true);
    this.timezoneError.set(null);
    this.family
      .updateTimezone(zone)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.auth.setTimezone(res.timezone);
          this.timezoneSaving.set(false);
        },
        error: () => {
          this.timezoneSaving.set(false);
          this.timezoneError.set('settings.general.timezone_error');
        },
      });
  }

  /** Renames the family. Only the parent holds family.manage, the same permission the server checks. */
  saveFamilyName(): void {
    if (this.familySaving()) return;
    const name = this.familyDraft.trim();
    if (!name) {
      this.familyError.set('family.name_edit.required');
      return;
    }
    if (name === this.familyName()) return;
    this.familySaving.set(true);
    this.familyError.set(null);
    this.family
      .updateFamilyName(name)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.auth.setFamilyName(res.family_name);
          this.familyDraft = res.family_name;
          this.familySaving.set(false);
        },
        error: () => {
          this.familySaving.set(false);
          this.familyError.set('family.name_edit.error');
        },
      });
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.loadApiError.set(null);
    this.weather
      .settings()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (s) => {
          this.fill(s);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(settingsErrorKey(err));
          this.loadApiError.set(err);
          this.loadState.set('error');
        },
      });
  }

  save(): void {
    void this.saveChanges();
  }

  hasUnsavedChanges(): boolean {
    return this.loadState() === 'ready' && this.snapshot() !== this.baseline;
  }

  saveChanges(): Promise<boolean> {
    if (this.busy()) return Promise.resolve(false);
    this.saved.set(false);
    this.saveError.set(null);

    const place = this.placeName.trim();
    const hasLat = this.latitude !== null;
    const hasLon = this.longitude !== null;
    const hasCoords = hasLat && hasLon;
    // Both coordinates or neither. Clearing both removes the location, including the place name.
    if (hasLat !== hasLon) {
      this.saveError.set('settings.general.error.coords_pair');
      return Promise.resolve(false);
    }
    if (hasCoords && !this.inRange(this.latitude, 90)) {
      this.saveError.set('settings.general.error.invalid_latitude');
      return Promise.resolve(false);
    }
    if (hasCoords && !this.inRange(this.longitude, 180)) {
      this.saveError.set('settings.general.error.invalid_longitude');
      return Promise.resolve(false);
    }

    return this.submit({
      location_name: hasCoords && place !== '' ? place : null,
      latitude: hasCoords ? this.latitude : null,
      longitude: hasCoords ? this.longitude : null,
      temperature_unit: this.unit,
      animation: this.animation,
    });
  }

  /** Fills the coordinates from the browser's location. The user still saves the form. */
  async detect(): Promise<void> {
    if (this.detecting() || this.busy()) return;
    this.detectError.set(null);
    this.detecting.set(true);
    try {
      const pos = await currentDeviceLocation();
      this.saved.set(false);
      this.latitude = roundCoord(pos.latitude);
      this.longitude = roundCoord(pos.longitude);
    } catch (kind) {
      this.detectError.set(`settings.general.detect_error.${kind}`);
    } finally {
      this.detecting.set(false);
    }
  }

  removeLocation(): void {
    if (this.busy()) return;
    this.saved.set(false);
    this.saveError.set(null);
    this.submit({
      location_name: null,
      latitude: null,
      longitude: null,
      temperature_unit: this.unit,
      animation: this.animation,
    });
  }

  private submit(input: WeatherSettings): Promise<boolean> {
    this.busy.set(true);
    return new Promise<boolean>((resolve) => {
      this.weather
        .saveSettings(input)
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => this.busy.set(false)),
        )
        .subscribe({
          next: (s) => {
            this.fill(s);
            this.saved.set(true);
            resolve(true);
          },
          error: (err: ApiError) => {
            this.saveError.set(this.errorKey(err));
            resolve(false);
          },
        });
    });
  }

  private fill(s: WeatherSettings): void {
    this.placeName = s.location_name ?? '';
    this.latitude = s.latitude;
    this.longitude = s.longitude;
    this.unit = s.temperature_unit;
    this.animation = s.animation;
    this.hasLocation.set(s.latitude !== null && s.longitude !== null);
    this.baseline = this.snapshot();
  }

  private snapshot(): string {
    return JSON.stringify([this.placeName, this.latitude, this.longitude, this.unit, this.animation]);
  }

  private inRange(value: number | null, limit: number): boolean {
    return value !== null && Number.isFinite(value) && value >= -limit && value <= limit;
  }

  private errorKey(err: ApiError): string {
    if (err.code && WEATHER_CODES.includes(err.code)) return `settings.general.error.${err.code}`;
    return settingsErrorKey(err);
  }
}
