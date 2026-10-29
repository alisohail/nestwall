import { Component, DestroyRef, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../core/auth/auth.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { OwnPasswordComponent } from '../../shared/own-password/own-password.component';
import { PhotoEditorComponent } from '../../shared/photo-editor/photo-editor.component';
import { IconComponent } from '../../shared/ui/icon/icon.component';
import { SessionsComponent } from '../sessions/sessions.component';
import { MyDetailsComponent } from '../me/my-details/my-details.component';

/** The parent's own profile page (`/profile`), laid out as in the profile mock. */
@Component({
    selector: 'app-profile',
    imports: [
        TranslocoModule,
        AvatarComponent,
        PhotoEditorComponent,
        IconComponent,
        MyDetailsComponent,
        OwnPasswordComponent,
        SessionsComponent,
    ],
    templateUrl: './profile.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './profile.component.scss'
})
export class ProfileComponent {
  auth = inject(AuthService);
  private destroyRef = inject(DestroyRef);

  showPicture = signal(false);

  /** Re-reads the session so the shell and this page show the new picture. */
  onPictureChanged(): void {
    this.auth
      .checkAuth()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe();
  }
}
