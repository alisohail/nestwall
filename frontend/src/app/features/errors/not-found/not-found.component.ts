import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { ErrorPageComponent } from '../../../shared/ui/error-page/error-page.component';

/** Shown for unknown routes and for records that no longer exist (HTTP 404). */
@Component({
    selector: 'app-not-found',
    imports: [ErrorPageComponent, RouterLink, TranslocoModule],
    templateUrl: './not-found.component.html',
    styleUrl: './not-found.component.scss'
})
export class NotFoundComponent {}
