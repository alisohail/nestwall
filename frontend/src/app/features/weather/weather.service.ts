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
   * coordinates when the family has no location; it never stores or logs them.
   */
  forecast(device?: DeviceCoords): Observable<WeatherResponse> {
    if (!device) return this.api.get<WeatherResponse>('/api/v1/weather');
    const query = `lat=${device.latitude.toFixed(4)}&lon=${device.longitude.toFixed(4)}`;
    return this.api.get<WeatherResponse>(`/api/v1/weather?${query}`);
  }

  settings(): Observable<WeatherSettings> {
    return this.api.get<WeatherSettings>('/api/v1/family/weather-settings');
  }

  saveSettings(input: WeatherSettings): Observable<WeatherSettings> {
    return this.api.put<WeatherSettings>('/api/v1/family/weather-settings', input);
  }
}
