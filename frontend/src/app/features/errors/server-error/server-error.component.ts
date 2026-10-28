import { Component, ChangeDetectionStrategy } from '@angular/core';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { ErrorPageComponent } from '../../../shared/ui/error-page/error-page.component';

/** Shown when the server fails (HTTP 5xx). The user can retry the page. */
@Component({
    selector: 'app-server-error',
    imports: [ErrorPageComponent, RouterLink, TranslocoModule],
    templateUrl: './server-error.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './server-error.component.scss'
})
export class ServerErrorComponent {
  tryAgain(): void {
    window.location.reload();
  }
}
