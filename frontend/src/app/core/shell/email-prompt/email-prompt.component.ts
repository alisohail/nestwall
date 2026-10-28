import { Component, HostListener, inject } from '@angular/core';
import { Router } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../auth/auth.service';
import { IconComponent } from '../../../shared/ui/icon/icon.component';

/** Asks a parent who has no email to add one, once after login. Later closes it for this session. */
@Component({
    selector: 'app-email-prompt',
    imports: [TranslocoModule, IconComponent],
    templateUrl: './email-prompt.component.html',
    styleUrl: './email-prompt.component.scss'
})
export class EmailPromptComponent {
  auth = inject(AuthService);
  private router = inject(Router);

  @HostListener('document:keydown.escape')
  later(): void {
    this.auth.dismissEmailPrompt();
  }

  setEmail(): void {
    const memberId = this.auth.currentUser()?.family?.member_id;
    this.auth.dismissEmailPrompt();
    if (memberId) {
      this.router.navigate(['/family', 'members', memberId]);
    }
  }
}
