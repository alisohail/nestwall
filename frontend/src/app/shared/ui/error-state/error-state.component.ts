import { Component, input, output } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { BannerComponent } from '../banner/banner.component';

/** Inline error with a retry action. Emits `retry` when the user presses the button. */
@Component({
    selector: 'app-error-state',
    imports: [TranslocoModule, BannerComponent],
    templateUrl: './error-state.component.html',
    styleUrl: './error-state.component.scss'
})
export class ErrorStateComponent {
  readonly messageKey = input.required<string>();
  readonly retryKey = input<string>('common.retry');
  readonly retry = output<void>();
}
