import { Component, inject, ChangeDetectionStrategy } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { BusyService } from '../../../core/busy/busy.service';

/** Bar pinned to the top of the screen after a failed save, so the user can see what went wrong. */
@Component({
    selector: 'app-save-error-bar',
    imports: [TranslocoModule],
    templateUrl: './save-error-bar.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './save-error-bar.component.scss'
})
export class SaveErrorBarComponent {
  readonly busy = inject(BusyService);
}
