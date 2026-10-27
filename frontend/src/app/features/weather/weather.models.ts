/** Weather conditions the backend maps from WMO codes. Anything else is shown as "unknown". */
export type WeatherCondition =
  | 'clear'
  | 'partly_cloudy'
  | 'cloudy'
  | 'fog'
  | 'drizzle'
  | 'rain'
  | 'snow'
  | 'storm'
  | 'unknown';

/** "c" for Celsius, "f" for Fahrenheit. */
export type TemperatureUnit = 'c' | 'f';

/** "calm" keeps the scene gentle; "live" moves more and is shown on every weather tile. */
export type WeatherAnimation = 'calm' | 'live';

export interface WeatherCurrent {
  temperature: number;
  feels_like: number;
  condition: WeatherCondition;
}

export interface WeatherDay {
  high: number;
  low: number;
  condition: WeatherCondition;
  precip_chance: number | null;
}

/** One hour of the forecast. `time` is an RFC 3339 timestamp with the place's UTC offset. */
export interface WeatherHour {
  time: string;
  temperature: number;
  condition: WeatherCondition;
  precip_chance: number | null;
}

export interface WeatherForecast {
  current: WeatherCurrent;
  today: WeatherDay;
  tomorrow: WeatherDay;
  hourly: WeatherHour[];
  fetched_at: string;
  stale: boolean;
}

/** Where the forecast's coordinates came from. Device forecasts are for the viewer only. */
export type WeatherSource = 'family' | 'device';

/** GET /api/v1/weather. `configured` is false until a location is known. */
export interface WeatherResponse {
  configured: boolean;
  source?: WeatherSource;
  location_name: string | null;
  temperature_unit: TemperatureUnit;
  animation: WeatherAnimation;
  forecast: WeatherForecast | null;
}

/** GET and PUT /api/v1/family/weather-settings. */
export interface WeatherSettings {
  location_name: string | null;
  latitude: number | null;
  longitude: number | null;
  temperature_unit: TemperatureUnit;
  animation: WeatherAnimation;
}
