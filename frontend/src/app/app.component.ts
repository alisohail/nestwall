import { Component, inject } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { ThemeService } from './core/theme/theme.service';
import { PwaPromptsComponent } from './core/pwa/pwa-prompts/pwa-prompts.component';
import { BusyService } from './core/busy/busy.service';
import { BusyOverlayComponent } from './shared/ui/busy-overlay/busy-overlay.component';
import { SaveErrorBarComponent } from './shared/ui/save-error-bar/save-error-bar.component';
import { UnsavedChangesDialogComponent } from './shared/ui/unsaved-changes-dialog/unsaved-changes-dialog.component';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [RouterOutlet, PwaPromptsComponent, BusyOverlayComponent, SaveErrorBarComponent, UnsavedChangesDialogComponent],
  templateUrl: './app.component.html',
  styleUrl: './app.component.scss',
})
export class AppComponent {
  // Applies the saved or system theme on every page, including ones outside the shell.
  private readonly theme = inject(ThemeService);
  readonly busy = inject(BusyService);
}

