import { Component, ChangeDetectionStrategy } from '@angular/core';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { NATIVE_SERVER_PICKER_URL, isNativeShell } from '../../../core/native/native-shell';

/** One row of the Configuration menu. A null path means the page is not built yet. */
export interface SettingsHubItem {
  key: string;
  path: string | null;
  labelKey: string;
  icon: string;
}

/** Settings tab: the Configuration menu. Profile and sign out stay in the top bar. */
@Component({
    selector: 'app-settings-hub',
    imports: [RouterLink, TranslocoModule],
    templateUrl: './settings-hub.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './settings-hub.component.scss'
})
export class SettingsHubComponent {
  /**
   * Built pages come first, then the rest of the mock order. Points and Badges are not in the mock
   * but are kept so their pages stay reachable.
   */
  /** Only the Android app has a server picker, so the row is hidden in the browser. */
  readonly isNative = isNativeShell();
  readonly serverPickerUrl = NATIVE_SERVER_PICKER_URL;

  readonly familySetup: readonly SettingsHubItem[] = [
    { key: 'general', path: 'general', labelKey: 'settings.nav.general', icon: 'tune' },
    { key: 'templates', path: 'templates', labelKey: 'settings.nav.templates', icon: 'description' },
    { key: 'categories', path: 'categories', labelKey: 'settings.nav.categories', icon: 'category' },
    { key: 'points', path: 'points', labelKey: 'settings.nav.points', icon: 'emoji_events' },
    { key: 'badges', path: 'badges', labelKey: 'settings.nav.badges', icon: 'star' },
    { key: 'calendars', path: 'calendars', labelKey: 'settings.nav.calendars', icon: 'calendar_month' },
    { key: 'notifications', path: 'notifications', labelKey: 'settings.nav.notifications', icon: 'notifications' },
    { key: 'email', path: 'email', labelKey: 'settings.nav.email', icon: 'mail' },
    { key: 'wall', path: 'wall', labelKey: 'settings.nav.wall', icon: 'monitor' },
    { key: 'integrations', path: 'integrations', labelKey: 'settings.nav.integrations', icon: 'link' },
    { key: 'data', path: 'data', labelKey: 'settings.nav.data', icon: 'download' },
  ];
}
