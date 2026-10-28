import { Component, computed, input, ChangeDetectionStrategy } from '@angular/core';

const KNOWN_CONDITIONS = ['clear', 'partly_cloudy', 'cloudy', 'fog', 'drizzle', 'rain', 'snow', 'storm'];

/** Animated weather icon. Each condition has its own motion; reduced motion shows the still icon. */
@Component({
  selector: 'app-weather-icon',
  standalone: true,
  templateUrl: './weather-icon.component.html',
  styleUrl: './weather-icon.component.scss',
  changeDetection: ChangeDetectionStrategy.Eager,
  host: {
    '[style.width.px]': 'size()',
    '[style.height.px]': 'size()',
  },
})
export class WeatherIconComponent {
  readonly condition = input<string>('unknown');
  readonly size = input(56);
  /** At night a clear or partly cloudy sky shows the moon instead of the sun. */
  readonly night = input(false);

  /** Unrecognised conditions fall back to the neutral cloud. */
  readonly kind = computed(() => (KNOWN_CONDITIONS.includes(this.condition()) ? this.condition() : 'unknown'));
}
