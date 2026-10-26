import { Component, HostListener, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { NgTemplateOutlet } from '@angular/common';
import { NavigationEnd, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { filter, map, startWith } from 'rxjs';
import { SettingsHeaderService } from './settings-header.service';

export interface SettingsSection {
  /** Null for pages not built yet. Those rows show as "Coming soon" and are not links. */
  path: string | null;
  labelKey: string;
  subKey?: string;
  icon: string;
}

export type BuiltSettingsSection = SettingsSection & { path: string; subKey: string };

/** Same order as the Configuration menu on the settings hub. */
export const SETTINGS_SECTIONS: readonly SettingsSection[] = [
  { path: 'general', labelKey: 'settings.nav.general', subKey: 'settings.sub.general', icon: 'tune' },
  { path: 'templates', labelKey: 'settings.nav.templates', subKey: 'settings.sub.templates', icon: 'list' },
  { path: 'categories', labelKey: 'settings.nav.categories', subKey: 'settings.sub.categories', icon: 'list' },
  { path: 'points', labelKey: 'settings.nav.points', subKey: 'settings.sub.points', icon: 'emoji_events' },
  { path: 'badges', labelKey: 'settings.nav.badges', subKey: 'settings.sub.badges', icon: 'star' },
  { path: 'calendars', labelKey: 'settings.nav.calendars', subKey: 'settings.sub.calendars', icon: 'calendar_month' },
  { path: 'notifications', labelKey: 'settings.nav.notifications', subKey: 'settings.sub.notifications', icon: 'notifications' },
  { path: 'email', labelKey: 'settings.nav.email', subKey: 'settings.sub.email', icon: 'mail' },
  { path: 'wall', labelKey: 'settings.nav.wall', subKey: 'settings.sub.wall', icon: 'monitor' },
  { path: 'integrations', labelKey: 'settings.nav.integrations', subKey: 'settings.sub.integrations', icon: 'link' },
  { path: 'data', labelKey: 'settings.nav.data', subKey: 'settings.sub.data', icon: 'download' },
  { path: 'diagnostics', labelKey: 'settings.nav.diagnostics', subKey: 'settings.sub.diagnostics', icon: 'monitor_heart' },
];

const BUILT_SECTIONS = SETTINGS_SECTIONS.filter(
  (s): s is BuiltSettingsSection => s.path !== null && s.subKey !== undefined,
);

/** Settings layout. Desktop shows a subnav; tablets open it as a dropdown; phones use a bottom sheet. */
@Component({
  selector: 'app-settings',
  standalone: true,
  imports: [NgTemplateOutlet, RouterOutlet, RouterLink, RouterLinkActive, TranslocoModule],
  templateUrl: './settings.component.html',
  styleUrl: './settings.component.scss',
})
export class SettingsComponent {
  private router = inject(Router);
  protected header = inject(SettingsHeaderService);

  readonly sections = SETTINGS_SECTIONS;
  sheetOpen = signal(false);

  private url = toSignal(
    this.router.events.pipe(
      filter((event): event is NavigationEnd => event instanceof NavigationEnd),
      map((event) => event.urlAfterRedirects),
      startWith(this.router.url),
    ),
    { initialValue: this.router.url },
  );

  active = computed<BuiltSettingsSection>(
    () => BUILT_SECTIONS.find((s) => this.url().includes(`/settings/admin/${s.path}`)) ?? BUILT_SECTIONS[0],
  );

  @HostListener('document:keydown.escape')
  closeSheet(): void {
    this.sheetOpen.set(false);
  }

  @HostListener('document:click', ['$event.target'])
  closeOnOutsideClick(target: EventTarget | null): void {
    if (!this.sheetOpen() || !(target instanceof Element)) {
      return;
    }
    if (target.closest('.section-picker, .sheet')) {
      return;
    }
    this.sheetOpen.set(false);
  }
}
