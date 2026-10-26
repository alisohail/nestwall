import { Component, input } from '@angular/core';

/** Placeholder rows shown while a list loads. Each row is an avatar and two lines. */
@Component({
  selector: 'app-skeleton',
  standalone: true,
  templateUrl: './skeleton.component.html',
  styleUrl: './skeleton.component.scss',
})
export class SkeletonComponent {
  readonly rows = input<number>(3);

  rowIndexes(): number[] {
    return Array.from({ length: this.rows() }, (_, i) => i);
  }
}
