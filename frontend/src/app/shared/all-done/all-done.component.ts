import { Component, input } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';

/** The all done card shown when nothing is left to do today. Used by Today and the My tasks Today tab. */
@Component({
  selector: 'app-all-done',
  standalone: true,
  imports: [TranslocoModule],
  templateUrl: './all-done.component.html',
  styleUrl: './all-done.component.scss',
})
export class AllDoneComponent {
  /** The child's first name for the message. */
  name = input<string>('');
  streakDays = input<number>(0);
}
