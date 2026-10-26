import { Component, inject } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';

/**
 * Stand-in screen for navigation targets that a later milestone builds.
 * The route supplies the label key via `data.titleKey`.
 */
@Component({
  selector: 'app-placeholder',
  standalone: true,
  imports: [TranslocoModule],
  templateUrl: './placeholder.component.html',
})
export class PlaceholderComponent {
  private route = inject(ActivatedRoute);

  readonly titleKey: string = (this.route.snapshot.data['titleKey'] as string) ?? 'shell.nav.dashboard';
}
