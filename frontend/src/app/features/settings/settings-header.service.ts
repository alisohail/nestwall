import { Injectable, TemplateRef, signal } from '@angular/core';

/** Lets the active settings page place its actions in the shared page header. */
@Injectable({ providedIn: 'root' })
export class SettingsHeaderService {
  readonly actions = signal<TemplateRef<unknown> | null>(null);
}
