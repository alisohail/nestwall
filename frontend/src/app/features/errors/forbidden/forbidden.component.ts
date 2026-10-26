import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { ErrorPageComponent } from '../../../shared/ui/error-page/error-page.component';

/** Shown when the user opens a page or action they are not allowed to use (HTTP 403). */
@Component({
  selector: 'app-forbidden',
  standalone: true,
  imports: [ErrorPageComponent, RouterLink, TranslocoModule],
  templateUrl: './forbidden.component.html',
  styleUrl: './forbidden.component.scss',
})
export class ForbiddenComponent {
  goBack(): void {
    history.back();
  }
}
