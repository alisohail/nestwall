import { Component, input, ChangeDetectionStrategy } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';

/** Empty screen or section: icon, title, optional body, and optional actions (projected). */
@Component({
    selector: 'app-empty-state',
    imports: [TranslocoModule],
    templateUrl: './empty-state.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './empty-state.component.scss'
})
export class EmptyStateComponent {
  readonly icon = input<string>('inbox');
  readonly titleKey = input.required<string>();
  readonly bodyKey = input<string | null>(null);
}
