import { Component, input, output, ChangeDetectionStrategy } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';

/** Confirmation step for actions that change a copy, such as completing or skipping it. */
@Component({
    selector: 'app-task-confirm',
    imports: [TranslocoModule],
    templateUrl: './task-confirm.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    host: { '(document:keydown.escape)': 'cancelled.emit()' }
})
export class TaskConfirmComponent {
  heading = input.required<string>();
  message = input<string>('');
  confirmLabel = input.required<string>();
  busy = input(false);

  confirmed = output<void>();
  cancelled = output<void>();
}
