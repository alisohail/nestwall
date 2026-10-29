import { Component, inject, ChangeDetectionStrategy } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { LoadingComponent } from '../loading/loading.component';
import { BusyService } from '../../../core/busy/busy.service';

/** Full-screen blocker shown while a save is running. It stops every click until the save finishes. */
@Component({
    selector: 'app-busy-overlay',
    imports: [TranslocoModule, LoadingComponent],
    templateUrl: './busy-overlay.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './busy-overlay.component.scss'
})
export class BusyOverlayComponent {
  readonly busy = inject(BusyService);
}
