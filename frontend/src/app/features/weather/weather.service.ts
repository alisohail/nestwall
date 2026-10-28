import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import { DeviceCoords } from './device-location';
import { WeatherResponse, WeatherSettings } from './weather.models';

@Injectable({ providedIn: 'root' })
export class WeatherService {
  private api = inject(ApiClient);

  /**
   * The family forecast. Any signed-in member can read it. With `device`, the server uses those
   * coordinates when the family has no location; it never stores or logs them. With `refresh`, the
   * server fetches a new forecast instead of its cached one.
   */
  forecast(device?: DeviceCoords, refresh = false): Observable<WeatherResponse> {
    const params: string[] = [];
    if (device) {
      params.push(`lat=${device.latitude.toFixed(4)}`, `lon=${device.longitude.toFixed(4)}`);
    }
    if (refresh) params.push('refresh=1');
    return this.api.get<WeatherResponse>(params.length ? `/api/v1/weather?${params.join('&')}` : '/api/v1/weather');
  }

  settings(): Observable<WeatherSettings> {
    return this.api.get<WeatherSettings>('/api/v1/family/weather-settings');
  }

  saveSettings(input: WeatherSettings): Observable<WeatherSettings> {
    return this.api.put<WeatherSettings>('/api/v1/family/weather-settings', input);
  }
}
