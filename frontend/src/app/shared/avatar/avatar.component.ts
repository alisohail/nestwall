import { Component, computed, inject, input } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { AvatarService } from './avatar.service';

/** chip 20px (inside a stat chip), xs 26px, sm 32px, list 36px, nav 44px (matches the top bar buttons), md 64px, xl 120px. */
export type AvatarDisplaySize = 'chip' | 'xs' | 'sm' | 'list' | 'nav' | 'md' | 'lg' | 'xl';

/** Shows a member's picture, or their initial on the member colour when there is no picture. */
@Component({
  selector: 'app-avatar',
  standalone: true,
  imports: [TranslocoModule],
  templateUrl: './avatar.component.html',
  styleUrl: './avatar.component.scss',
  host: { '[attr.title]': 'name()' },
})
export class AvatarComponent {
  private avatars = inject(AvatarService);

  name = input.required<string>();
  colour = input<string>('#888888');
  avatarId = input<string | null>(null);
  size = input<AvatarDisplaySize>('md');

  sizeClass = computed(() => `avatar size-${this.size()}`);

  /** The stored pictures come in two sizes. The large one is used where the picture is shown big. */
  src = computed(() => {
    const id = this.avatarId();
    return id ? this.avatars.url(id, this.size() === 'xl' ? 256 : 96) : null;
  });

  initial = computed(() => this.name().charAt(0).toUpperCase());
}
