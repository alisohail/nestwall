import { Component, computed, input } from '@angular/core';

const KNOWN_CONDITIONS = ['clear', 'partly_cloudy', 'cloudy', 'fog', 'drizzle', 'rain', 'snow', 'storm'];

/** Animated weather icon. Each condition has its own motion; reduced motion shows the still icon. */
@Component({
  selector: 'app-weather-icon',
  standalone: true,
  templateUrl: './weather-icon.component.html',
  styleUrl: './weather-icon.component.scss',
  host: {
    '[style.width.px]': 'size()',
    '[style.height.px]': 'size()',
  },
})
export class WeatherIconComponent {
  readonly condition = input<string>('unknown');
  readonly size = input(56);

  /** Unrecognised conditions fall back to the neutral cloud. */
  readonly kind = computed(() => (KNOWN_CONDITIONS.includes(this.condition()) ? this.condition() : 'unknown'));
}
