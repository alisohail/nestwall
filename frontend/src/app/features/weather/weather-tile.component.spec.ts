import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { WeatherTileComponent, dayPartFor } from './weather-tile.component';
import { WeatherForecast, WeatherResponse } from './weather.models';
import { WeatherService } from './weather.service';

const forecast: WeatherForecast = {
  current: { temperature: 9, feels_like: 7, condition: 'rain' },
  today: { high: 11, low: 4, condition: 'rain', precip_chance: 60 },
  tomorrow: { high: 13, low: 6, condition: 'clear', precip_chance: null },
  hourly: Array.from({ length: 10 }, (_, i) => ({
    time: `2025-01-01T${String(10 + i).padStart(2, '0')}:00:00+00:00`,
    temperature: 9 + i,
    condition: i % 2 ? 'clear' : 'rain',
    precip_chance: i === 0 ? 60 : null,
  })),
  fetched_at: '2025-01-01T00:00:00Z',
  stale: false,
};

const familyWeather: WeatherResponse = {
  configured: true,
  source: 'family',
  location_name: 'Toronto',
  temperature_unit: 'c',
  animation: 'calm',
  forecast,
};

const unset: WeatherResponse = {
  configured: false,
  location_name: null,
  temperature_unit: 'c',
  animation: 'calm',
  forecast: null,
};

const deviceWeather: WeatherResponse = {
  configured: true,
  source: 'device',
  location_name: 'Paris, France',
  temperature_unit: 'c',
  animation: 'calm',
  forecast,
};

describe('WeatherTileComponent', () => {
  let fixture: ComponentFixture<WeatherTileComponent>;
  let weather: jasmine.SpyObj<WeatherService>;
  let geolocation: jasmine.Spy;

  function build(): void {
    fixture = TestBed.createComponent(WeatherTileComponent);
    fixture.detectChanges();
  }

  async function settle(): Promise<void> {
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function state(): string | null {
    const el = fixture.nativeElement.querySelector('[data-testid="weather-tile"]') as HTMLElement | null;
    return el?.getAttribute('data-state') ?? null;
  }

  /** Stubs navigator.geolocation.getCurrentPosition with a position or a PERMISSION_DENIED error. */
  function stubBrowser(result: { latitude: number; longitude: number } | 'denied'): void {
    geolocation = spyOn(navigator.geolocation, 'getCurrentPosition').and.callFake(
      (success: PositionCallback, failure?: PositionErrorCallback | null) => {
        if (result === 'denied') {
          failure?.({
            code: 1,
            message: 'denied',
            PERMISSION_DENIED: 1,
            POSITION_UNAVAILABLE: 2,
            TIMEOUT: 3,
          } as GeolocationPositionError);
          return;
        }
        success({ coords: { ...result, accuracy: 10 } } as GeolocationPosition);
      },
    );
  }

  beforeEach(async () => {
    weather = jasmine.createSpyObj<WeatherService>('WeatherService', ['forecast']);

    await TestBed.configureTestingModule({
      imports: [WeatherTileComponent, getTranslocoTestingModule()],
      providers: [provideRouter([]), { provide: WeatherService, useValue: weather }],
    }).compileComponents();
  });

  it('shows hourly temps in both variants, with the compact card capped at 8 hours', async () => {
    weather.forecast.and.returnValue(of(familyWeather));
    stubBrowser('denied');
    fixture = TestBed.createComponent(WeatherTileComponent);
    fixture.componentRef.setInput('variant', 'full');
    fixture.detectChanges();
    await settle();

    const wideHours = fixture.nativeElement.querySelectorAll('[data-testid="weather-hour"]') as NodeListOf<HTMLElement>;
    expect(wideHours.length).toBe(10);
    expect(wideHours[0].classList).toContain('now');
    expect(wideHours[0].textContent).toContain('Now');

    fixture.componentRef.setInput('variant', 'compact');
    fixture.detectChanges();
    const compactHours = fixture.nativeElement.querySelectorAll('[data-testid="weather-hour"]');
    expect(compactHours.length).toBe(8);
  });

  it('shows the family forecast without asking the browser for a location', async () => {
    weather.forecast.and.returnValue(of(familyWeather));
    stubBrowser({ latitude: 48.8566, longitude: 2.3522 });
    build();
    await settle();

    expect(state()).toBe('ready');
    expect(weather.forecast).toHaveBeenCalledTimes(1);
    expect(weather.forecast).toHaveBeenCalledWith();
    expect(geolocation).not.toHaveBeenCalled();
    expect(fixture.nativeElement.textContent).toContain('Toronto');
  });

  it('uses the viewer device location when the family has no location', async () => {
    weather.forecast.and.returnValues(of(unset), of(deviceWeather));
    stubBrowser({ latitude: 48.8566, longitude: 2.3522 });
    build();
    await settle();

    expect(state()).toBe('ready');
    expect(weather.forecast).toHaveBeenCalledTimes(2);
    expect(weather.forecast).toHaveBeenCalledWith({ latitude: 48.8566, longitude: 2.3522 });
    expect(fixture.nativeElement.querySelector('.wx-place')?.textContent).toContain('Paris, France');
  });

  it('stays unconfigured when the browser denies location', async () => {
    weather.forecast.and.returnValue(of(unset));
    stubBrowser('denied');
    build();
    await settle();

    expect(state()).toBe('unconfigured');
    expect(weather.forecast).toHaveBeenCalledTimes(1);
  });

  it('shows the error state when the device forecast fails', async () => {
    weather.forecast.and.returnValues(of(unset), throwError(() => new Error('upstream down')));
    stubBrowser({ latitude: 48.8566, longitude: 2.3522 });
    build();
    await settle();

    expect(state()).toBe('error');
  });

  it('shows today and tomorrow side by side in the compact variant', async () => {
    weather.forecast.and.returnValue(of(familyWeather));
    fixture = TestBed.createComponent(WeatherTileComponent);
    fixture.componentRef.setInput('variant', 'compact');
    fixture.detectChanges();
    await settle();

    const pair = fixture.nativeElement.querySelector('.wx-pair') as HTMLElement | null;
    expect(pair).not.toBeNull();
    expect(pair?.querySelectorAll('.wx-d').length).toBe(2);
    expect(pair?.textContent).toContain('60% rain');
    expect(fixture.nativeElement.querySelector('[data-testid="weather-tomorrow"]')?.textContent).toContain('13');
    expect(fixture.nativeElement.textContent).toContain('Toronto');
  });

  it('paints a background scene that follows the current condition and is hidden from assistive tech', async () => {
    weather.forecast.and.returnValue(of(familyWeather));
    build();
    await settle();

    const scene = fixture.nativeElement.querySelector('[data-testid="weather-scene"]') as HTMLElement;
    expect(scene.getAttribute('data-scene')).toBe('rain');
    expect(scene.getAttribute('aria-hidden')).toBe('true');
    expect(scene.querySelectorAll('.wx-particle').length).toBeGreaterThan(0);
  });

  it('chooses the scene from the condition and falls back to none for unknown conditions', async () => {
    const clear: WeatherResponse = {
      ...familyWeather,
      forecast: { ...forecast, current: { temperature: 20, feels_like: 19, condition: 'clear' } },
    };
    weather.forecast.and.returnValue(of(clear));
    build();
    await settle();
    expect(fixture.nativeElement.querySelector('[data-testid="weather-scene"]')?.getAttribute('data-scene')).toBe(
      'sun',
    );

    const unknown: WeatherResponse = {
      ...familyWeather,
      forecast: { ...forecast, current: { temperature: 5, feels_like: 5, condition: 'unknown' } },
    };
    weather.forecast.and.returnValue(of(unknown));
    fixture = TestBed.createComponent(WeatherTileComponent);
    fixture.detectChanges();
    await settle();
    expect(fixture.nativeElement.querySelector('[data-testid="weather-scene"]')?.getAttribute('data-scene')).toBe(
      'none',
    );
  });

  it('shows no falling particles for sunny or cloudy scenes, even in live motion', async () => {
    for (const condition of ['clear', 'cloudy'] as const) {
      weather.forecast.and.returnValue(
        of({
          ...familyWeather,
          animation: 'live',
          forecast: { ...forecast, current: { temperature: 20, feels_like: 19, condition } },
        }),
      );
      build();
      await settle();

      const scene = fixture.nativeElement.querySelector('[data-testid="weather-scene"]') as HTMLElement;
      expect(scene.querySelectorAll('.wx-particle').length).toBe(0);
    }
  });

  it('marks the scene calm by default and uses fewer particles than live', async () => {
    weather.forecast.and.returnValue(of(familyWeather));
    build();
    await settle();

    const scene = fixture.nativeElement.querySelector('[data-testid="weather-scene"]') as HTMLElement;
    expect(scene.getAttribute('data-motion')).toBe('calm');
    expect(scene.querySelectorAll('.wx-particle').length).toBe(14);
  });

  it('switches the scene to live motion with more particles', async () => {
    weather.forecast.and.returnValue(of({ ...familyWeather, animation: 'live' }));
    build();
    await settle();

    const scene = fixture.nativeElement.querySelector('[data-testid="weather-scene"]') as HTMLElement;
    expect(scene.getAttribute('data-motion')).toBe('live');
    expect(scene.querySelectorAll('.wx-particle').length).toBe(30);
  });

  it('uses the drizzle scene for drizzle instead of the rain scene', async () => {
    weather.forecast.and.returnValue(
      of({ ...familyWeather, forecast: { ...forecast, current: { temperature: 8, feels_like: 6, condition: 'drizzle' } } }),
    );
    build();
    await settle();

    expect(fixture.nativeElement.querySelector('[data-testid="weather-scene"]')?.getAttribute('data-scene')).toBe(
      'drizzle',
    );
  });

  it('gives each particle its own length, size, sway and timing', async () => {
    weather.forecast.and.returnValue(of(familyWeather));
    build();
    await settle();

    const particles = fixture.nativeElement.querySelectorAll('.wx-particle') as NodeListOf<HTMLElement>;
    const styles = Array.from(particles).map((el) => el.getAttribute('style') ?? '');
    expect(styles.every((s) => s.includes('--len:') && s.includes('--base:') && s.includes('--sway:'))).toBeTrue();
    expect(new Set(styles).size).toBeGreaterThan(1);
  });

  it('marks the time of day from the current hour', () => {
    expect(dayPartFor(6)).toBe('dawn');
    expect(dayPartFor(13)).toBe('day');
    expect(dayPartFor(18)).toBe('dusk');
    expect(dayPartFor(23)).toBe('night');
    expect(dayPartFor(2)).toBe('night');
  });
});
