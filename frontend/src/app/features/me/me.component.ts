import { Component, DestroyRef, inject, signal, computed } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../core/auth/auth.service';
import { ThemeService } from '../../core/theme/theme.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { OwnPasswordComponent } from '../../shared/own-password/own-password.component';
import { PhotoEditorComponent } from '../../shared/photo-editor/photo-editor.component';
import { IconComponent } from '../../shared/ui/icon/icon.component';
import { MyBadgesCardComponent } from '../badges/my-badges-card/my-badges-card.component';
import { SessionsComponent } from '../sessions/sessions.component';
import { MyDetailsComponent } from './my-details/my-details.component';
import { MyPointsComponent } from './my-points/my-points.component';
import { MeProfileStatsComponent } from './profile-stats/profile-stats.component';

@Component({
    selector: 'app-me',
    imports: [
        TranslocoModule,
        RouterLink,
        SessionsComponent,
        AvatarComponent,
        PhotoEditorComponent,
        IconComponent,
        MyPointsComponent,
        MyBadgesCardComponent,
        MyDetailsComponent,
        OwnPasswordComponent,
        MeProfileStatsComponent,
    ],
    templateUrl: './me.component.html',
    styleUrl: './me.component.scss'
})
export class MeComponent {
  auth = inject(AuthService);
  private theme = inject(ThemeService);
  private destroyRef = inject(DestroyRef);

  showPicture = signal(false);

  isDark = computed(() => this.theme.currentTheme() === 'dark');

  /** Re-reads the session so the shell and this page show the new picture. */
  onPictureChanged(): void {
    this.auth
      .checkAuth()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe();
  }

  toggleTheme(): void {
    const next = this.isDark() ? 'light' : 'dark';
    this.theme.setTheme(next);
    // The choice still applies on this device if the save fails.
    this.auth.saveTheme(next).subscribe({ error: () => undefined });
  }
}
