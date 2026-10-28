import { Component, HostListener, inject } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { UnsavedChangesService } from '../../../core/unsaved/unsaved-changes';

/** Centred prompt shown when the user leaves a page with unsaved edits. Escape or backdrop keeps the user on the page. */
@Component({
    selector: 'app-unsaved-changes-dialog',
    imports: [TranslocoModule],
    templateUrl: './unsaved-changes-dialog.component.html'
})
export class UnsavedChangesDialogComponent {
  readonly unsaved = inject(UnsavedChangesService);

  @HostListener('document:keydown.escape')
  onEscape(): void {
    if (this.unsaved.pending()) this.unsaved.stay();
  }
}
