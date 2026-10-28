import { Component, computed, inject, input, ChangeDetectionStrategy } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { ThemeService } from '../../../core/theme/theme.service';

export type LoadingVariant = 'full' | 'inline' | 'spinner';

/**
 * Loading indicator. `full` shows the Nesty mark for page loads, `inline` shows a small
 * mark next to a label for sections, and `spinner` is a plain ring for buttons and tight spaces.
 * The mark stops animating when the user prefers reduced motion (see the stylesheet).
 */
@Component({
    selector: 'app-loading',
    imports: [TranslocoModule],
    templateUrl: './loading.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './loading.component.scss'
})
export class LoadingComponent {
  private readonly theme = inject(ThemeService);

  readonly variant = input<LoadingVariant>('inline');
  readonly labelKey = input<string>('loading.default');

  readonly logoUrl = computed(() =>
    this.theme.currentTheme() === 'dark' ? '/assets/images/nesty-dark.svg' : '/assets/images/nesty.svg'
  );
}
