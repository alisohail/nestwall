import { WritableSignal, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { Observable, of, throwError } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { FamilyService } from '../../family/family.service';
import { WeatherSettings } from '../../weather/weather.models';
import { WeatherService } from '../../weather/weather.service';
import { GeneralSettingsComponent } from './general-settings.component';

const saved: WeatherSettings = {
  location_name: 'Toronto',
  latitude: 43.65,
  longitude: -79.38,
  temperature_unit: 'c',
  animation: 'calm',
};

const empty: WeatherSettings = {
  location_name: null,
  latitude: null,
  longitude: null,
  temperature_unit: 'c',
  animation: 'calm',
};

describe('GeneralSettingsComponent', () => {
  let fixture: ComponentFixture<GeneralSettingsComponent>;
  let weather: jasmine.SpyObj<WeatherService>;
  let family: jasmine.SpyObj<FamilyService>;
  let auth: { currentUser: WritableSignal<{ family: { family_name: string } }>; loadPermissions: () => Observable<undefined>; can: () => boolean; setFamilyName: jasmine.Spy };

  function build(): void {
    fixture = TestBed.createComponent(GeneralSettingsComponent);
    fixture.detectChanges();
  }

  async function settle(): Promise<void> {
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function el<T extends Element = HTMLElement>(selector: string): T | null {
    return fixture.nativeElement.querySelector(selector) as T | null;
  }

  function type(selector: string, value: string): void {
    const input = el<HTMLInputElement>(selector);
    if (!input) throw new Error(`missing ${selector}`);
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  }

  beforeEach(async () => {
    weather = jasmine.createSpyObj<WeatherService>('WeatherService', ['settings', 'saveSettings']);
    weather.settings.and.returnValue(of(saved));
    weather.saveSettings.and.callFake((input: WeatherSettings) => of(input));
    auth = {
      currentUser: signal({ family: { family_name: 'Rivera' } }),
      loadPermissions: () => of(undefined),
      can: () => true,
      setFamilyName: jasmine.createSpy('setFamilyName'),
    };
    family = jasmine.createSpyObj<FamilyService>('FamilyService', ['updateFamilyName']);
    family.updateFamilyName.and.returnValue(of({ family_name: 'Chen family' }));

    await TestBed.configureTestingModule({
      imports: [GeneralSettingsComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        { provide: WeatherService, useValue: weather },
        { provide: AuthService, useValue: auth },
        { provide: FamilyService, useValue: family },
      ],
    }).compileComponents();
  });

  it('renames the family from the family name card', () => {
    build();

    type('[data-testid="family-name-input"]', '  Chen family  ');
    el<HTMLButtonElement>('[data-testid="family-name-save"]')?.click();
    fixture.detectChanges();

    expect(family.updateFamilyName).toHaveBeenCalledWith('Chen family');
    expect(auth.setFamilyName).toHaveBeenCalledWith('Chen family');
  });

  it('shows the load error screen and retry reloads the weather settings', () => {
    weather.settings.and.returnValue(throwError(() => ({ code: 'network', status: 0 }) as ApiError));
    build();

    expect(el('app-loading')).toBeNull();
    expect(el('app-load-error')).not.toBeNull();

    weather.settings.and.returnValue(of(saved));
    el<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
    fixture.detectChanges();

    expect(weather.settings).toHaveBeenCalledTimes(2);
    expect(el('app-load-error')).toBeNull();
  });

  it('shows the saved location, unit and animation', async () => {
    build();
    await settle();
    expect(el<HTMLInputElement>('[data-testid="weather-place"]')?.value).toBe('Toronto');
    expect(el<HTMLInputElement>('[data-testid="weather-lat"]')?.value).toBe('43.65');
    expect(el<HTMLInputElement>('[data-testid="weather-lon"]')?.value).toBe('-79.38');
    expect(el('[data-testid="weather-unit-c"]')?.getAttribute('aria-pressed')).toBe('true');
    expect(el('[data-testid="weather-animation-calm"]')?.getAttribute('aria-pressed')).toBe('true');
    expect(el('[data-testid="weather-animation-live"]')?.getAttribute('aria-pressed')).toBe('false');
  });

  it('saves Live when chosen and keeps Calm as the default', async () => {
    build();
    await settle();
    el<HTMLButtonElement>('[data-testid="weather-animation-live"]')?.click();
    fixture.detectChanges();
    expect(el('[data-testid="weather-animation-live"]')?.getAttribute('aria-pressed')).toBe('true');
    el<HTMLButtonElement>('[data-testid="weather-save"]')?.click();
    fixture.detectChanges();
    expect(weather.saveSettings).toHaveBeenCalledWith(jasmine.objectContaining({ animation: 'live' }));
  });

  it('shows the saved Live choice when the settings load', async () => {
    weather.settings.and.returnValue(of({ ...saved, animation: 'live' }));
    build();
    await settle();
    expect(el('[data-testid="weather-animation-live"]')?.getAttribute('aria-pressed')).toBe('true');
  });

  it('maps an invalid animation code to its message', async () => {
    weather.saveSettings.and.returnValue(throwError(() => ({ code: 'invalid_animation', status: 400 }) as ApiError));
    build();
    await settle();
    el<HTMLButtonElement>('[data-testid="weather-animation-live"]')?.click();
    fixture.detectChanges();
    el<HTMLButtonElement>('[data-testid="weather-save"]')?.click();
    fixture.detectChanges();
    expect(el('app-banner')?.textContent).toContain('Choose Calm or Live');
  });

  it('offers removal only when a location is saved', async () => {
    weather.settings.and.returnValue(of(empty));
    build();
    await settle();
    expect(el('[data-testid="weather-remove"]')).toBeNull();
  });

  it('asks for both coordinates before saving', async () => {
    build();
    await settle();
    type('[data-testid="weather-lon"]', '');
    el<HTMLButtonElement>('[data-testid="weather-save"]')?.click();
    fixture.detectChanges();
    expect(weather.saveSettings).not.toHaveBeenCalled();
    expect(el('app-banner')?.textContent).toContain('Enter both latitude and longitude');
  });

  it('rejects a latitude outside -90 to 90', async () => {
    build();
    await settle();
    type('[data-testid="weather-lat"]', '91');
    el<HTMLButtonElement>('[data-testid="weather-save"]')?.click();
    fixture.detectChanges();
    expect(weather.saveSettings).not.toHaveBeenCalled();
    expect(el('app-banner')?.textContent).toContain('Latitude must be between -90 and 90');
  });

  it('saves the location and the chosen unit', async () => {
    build();
    await settle();
    el<HTMLButtonElement>('[data-testid="weather-unit-f"]')?.click();
    fixture.detectChanges();
    el<HTMLButtonElement>('[data-testid="weather-save"]')?.click();
    fixture.detectChanges();
    expect(weather.saveSettings).toHaveBeenCalledWith({
      location_name: 'Toronto',
      latitude: 43.65,
      longitude: -79.38,
      temperature_unit: 'f',
      animation: 'calm',
    });
    expect(el('[role="status"]')?.textContent).toContain('Saved');
  });

  it('drops the place name when the coordinates are cleared together', async () => {
    build();
    await settle();
    type('[data-testid="weather-lat"]', '');
    type('[data-testid="weather-lon"]', '');
    el<HTMLButtonElement>('[data-testid="weather-save"]')?.click();
    fixture.detectChanges();
    expect(weather.saveSettings).toHaveBeenCalledWith({
      location_name: null,
      latitude: null,
      longitude: null,
      temperature_unit: 'c',
      animation: 'calm',
    });
  });

  it('removes the location by saving empty values', async () => {
    build();
    await settle();
    el<HTMLButtonElement>('[data-testid="weather-remove"]')?.click();
    fixture.detectChanges();
    expect(weather.saveSettings).toHaveBeenCalledWith({
      location_name: null,
      latitude: null,
      longitude: null,
      temperature_unit: 'c',
      animation: 'calm',
    });
  });

  it('maps a server validation code to its message', async () => {
    weather.saveSettings.and.returnValue(throwError(() => ({ code: 'invalid_longitude', status: 400 }) as ApiError));
    build();
    await settle();
    type('[data-testid="weather-lon"]', '-79.5');
    el<HTMLButtonElement>('[data-testid="weather-save"]')?.click();
    fixture.detectChanges();
    expect(el('app-banner')?.textContent).toContain('Longitude must be between -180 and 180');
  });

  it('fills the coordinates from the browser without saving', async () => {
    weather.settings.and.returnValue(of(empty));
    spyOn(navigator.geolocation, 'getCurrentPosition').and.callFake((success: PositionCallback) => {
      success({ coords: { latitude: 43.6532197, longitude: -79.3831843 } } as GeolocationPosition);
    });
    build();
    await settle();
    el<HTMLButtonElement>('[data-testid="weather-detect"]')?.click();
    await settle();
    expect(el<HTMLInputElement>('[data-testid="weather-lat"]')?.value).toBe('43.6532');
    expect(el<HTMLInputElement>('[data-testid="weather-lon"]')?.value).toBe('-79.3832');
    expect(weather.saveSettings).not.toHaveBeenCalled();
  });

  it('explains when the browser blocks location access', async () => {
    spyOn(navigator.geolocation, 'getCurrentPosition').and.callFake((_ok: PositionCallback, fail?: PositionErrorCallback | null) => {
      fail?.({ code: 1, PERMISSION_DENIED: 1, TIMEOUT: 3, POSITION_UNAVAILABLE: 2 } as GeolocationPositionError);
    });
    build();
    await settle();
    el<HTMLButtonElement>('[data-testid="weather-detect"]')?.click();
    await settle();
    expect(el('app-banner')?.textContent).toContain('Location access was blocked');
    expect(weather.saveSettings).not.toHaveBeenCalled();
  });
});
