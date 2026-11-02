import { Component, input, output, ChangeDetectionStrategy } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';

/** Asks the user to confirm a destructive action before it runs. Escape and clicking outside cancel. */
@Component({
    selector: 'app-confirm-dialog',
    imports: [TranslocoModule],
    templateUrl: './confirm-dialog.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    host: { '(document:keydown.escape)': 'cancelled.emit()' }
})
export class ConfirmDialogComponent {
  heading = input.required<string>();
  message = input<string>('');
  confirmLabel = input.required<string>();
  /** Styles the confirm button as a destructive action. */
  danger = input(true);
  busy = input(false);

  confirmed = output<void>();
  cancelled = output<void>();
}
