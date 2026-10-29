import { Component, input, ChangeDetectionStrategy } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';

export type ErrorPageTone = 'danger' | 'calm' | 'warm';

/**
 * Full-page error layout used by the 403, 404, and 500 screens.
 * Projected content is the action row (buttons or links).
 */
@Component({
    selector: 'app-error-page',
    imports: [TranslocoModule],
    templateUrl: './error-page.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './error-page.component.scss'
})
export class ErrorPageComponent {
  readonly icon = input.required<string>();
  readonly tone = input<ErrorPageTone>('danger');
  readonly code = input.required<string>();
  readonly titleKey = input.required<string>();
  readonly bodyKey = input.required<string>();
  readonly reference = input.required<string>();
}
