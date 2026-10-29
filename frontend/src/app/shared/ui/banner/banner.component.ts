import { Component, computed, input, ChangeDetectionStrategy } from '@angular/core';

export type BannerTone = 'info' | 'success' | 'warning' | 'error';

const DEFAULT_ICON: Record<BannerTone, string> = {
  info: 'info',
  success: 'check_circle',
  warning: 'warning',
  error: 'error',
};

/**
 * Inline status message. Errors and warnings announce as alerts,
 * other tones as polite status updates. Projected content is the message body.
 * The icon defaults to one matching the tone.
 */
@Component({
  selector: 'app-banner',
  standalone: true,
  templateUrl: './banner.component.html',
  changeDetection: ChangeDetectionStrategy.Eager,
  styleUrl: './banner.component.scss',
})
export class BannerComponent {
  readonly tone = input<BannerTone>('info');
  readonly icon = input<string | null>(null);

  readonly classes = computed(() => `nw-banner nw-banner--${this.tone()}`);
  readonly iconName = computed(() => this.icon() ?? DEFAULT_ICON[this.tone()]);
  readonly role = computed(() =>
    this.tone() === 'error' || this.tone() === 'warning' ? 'alert' : 'status'
  );
}
