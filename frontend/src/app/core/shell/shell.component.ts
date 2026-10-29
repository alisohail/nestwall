import { Component, HostListener, inject, computed, signal, ChangeDetectionStrategy } from '@angular/core';
import { RouterOutlet, RouterLink, RouterLinkActive, Router, NavigationEnd } from '@angular/router';
import { toSignal } from '@angular/core/rxjs-interop';
import { filter, map } from 'rxjs';
import { TranslocoModule } from '@ngneat/transloco';
import { ThemeService } from '../theme/theme.service';
import { AuthService } from '../auth/auth.service';
import { IconComponent, IconName } from '../../shared/ui/icon/icon.component';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { EmailPromptComponent } from './email-prompt/email-prompt.component';
import { NotificationBellComponent } from '../../features/notifications/notification-bell.component';

export interface NavItem {
  path: string;
  labelKey: string;
  icon: IconName;
}

/** Parent sidebar (tablet and desktop), mirrors PARENT_NAV in the mock app script. */
export const PARENT_NAV: NavItem[] = [
  { path: '/dashboard', labelKey: 'shell.nav.dashboard', icon: 'home' },
  { path: '/tasks', labelKey: 'shell.nav.tasks', icon: 'tasks' },
  { path: '/bonus', labelKey: 'shell.nav.bonus', icon: 'bolt' },
  { path: '/approvals', labelKey: 'shell.nav.approvals', icon: 'check' },
  { path: '/calendar', labelKey: 'shell.nav.calendar', icon: 'calendar' },
  { path: '/rewards', labelKey: 'shell.nav.rewards', icon: 'gift' },
  { path: '/points', labelKey: 'shell.nav.points', icon: 'star' },
  { path: '/family', labelKey: 'shell.nav.family', icon: 'users' },
  { path: '/settings', labelKey: 'shell.nav.settings', icon: 'settings' },
];

/** Parent phone bottom bar, in sidebar order. The bar shows as many as fit; the rest go under More. */
export const PARENT_MOBILE_NAV: NavItem[] = PARENT_NAV.map((item) =>
  item.path === '/dashboard' ? { ...item, labelKey: 'shell.nav.home' } : item
);

/** Width given to one bottom-bar destination in px. Mirrors the mock. */
export const MOBILE_NAV_SLOT_PX = 72;

/**
 * Splits the parent destinations into those shown in the bottom bar and those under More.
 * Shows all when they fit; otherwise keeps one slot for More and keeps sidebar order.
 */
export function splitMobileNav(
  items: NavItem[],
  viewportWidth: number
): { visible: NavItem[]; overflow: NavItem[] } {
  const slots = Math.floor(viewportWidth / MOBILE_NAV_SLOT_PX);
  if (slots >= items.length) {
    return { visible: items, overflow: [] };
  }
  const shown = Math.max(slots - 1, 0);
  return { visible: items.slice(0, shown), overflow: items.slice(shown) };
}

/**
 * Child layout (rail on tablet and desktop, tab bar on phone). The mock's Me tab is left out:
 * the avatar at the top right (phone) and bottom left (tablet and desktop) opens the profile.
 */
export const CHILD_NAV: NavItem[] = [
  { path: '/today', labelKey: 'shell.nav.today', icon: 'sun' },
  { path: '/my-tasks', labelKey: 'shell.nav.tasks', icon: 'tasks' },
  { path: '/calendar', labelKey: 'shell.nav.calendar', icon: 'calendar' },
  { path: '/rewards', labelKey: 'shell.nav.rewards', icon: 'gift' },
];

@Component({
    selector: 'app-shell',
    imports: [RouterOutlet, RouterLink, RouterLinkActive, TranslocoModule, IconComponent, AvatarComponent, EmailPromptComponent, NotificationBellComponent],
    templateUrl: './shell.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './shell.component.scss'
})
export class ShellComponent {
  private themeService = inject(ThemeService);
  private authService = inject(AuthService);
  private router = inject(Router);

  isChild = this.authService.isChild;

  /** Children open their Me page; parents open Profile. Both avatars link here. */
  profilePath = computed(() => (this.isChild() ? '/me' : '/profile'));

  sidebarItems = computed(() => (this.isChild() ? CHILD_NAV : PARENT_NAV));

  viewportWidth = signal(window.innerWidth);
  moreOpen = signal(false);

  /** Bottom-bar destinations that fit the current width. Children keep their four tabs. */
  private mobileSplit = computed(() =>
    this.isChild()
      ? { visible: CHILD_NAV, overflow: [] as NavItem[] }
      : splitMobileNav(PARENT_MOBILE_NAV, this.viewportWidth())
  );

  visibleMobileItems = computed(() => this.mobileSplit().visible);

  /** Parent destinations that do not fit the bar, in sidebar order, listed under More. */
  overflowItems = computed(() => this.mobileSplit().overflow);

  private currentUrl = toSignal(
    this.router.events.pipe(
      filter((event): event is NavigationEnd => event instanceof NavigationEnd),
      map((event) => event.urlAfterRedirects)
    ),
    { initialValue: this.router.url }
  );

  moreActive = computed(() => {
    const url = this.currentUrl();
    return this.overflowItems().some((item) => url === item.path || url.startsWith(item.path + '/'));
  });

  @HostListener('window:resize')
  onResize(): void {
    this.viewportWidth.set(window.innerWidth);
  }

  @HostListener('document:keydown.escape')
  closeMore(): void {
    this.moreOpen.set(false);
  }

  logoUrl = computed(() => {
    const isDark = this.themeService.currentTheme() === 'dark';
    return isDark ? '/assets/images/logo-dark.svg' : '/assets/images/logo.svg';
  });

  themeIcon = computed<IconName>(() =>
    this.themeService.currentTheme() === 'dark' ? 'moon' : 'sun'
  );

  /** Today's date for the top bar, for example "Fri, 9 Oct 2026". Hidden on phones. */
  today = new Intl.DateTimeFormat(undefined, {
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  }).format(new Date());

  displayName = computed(() => this.authService.currentUser()?.user?.display_name ?? '');

  /** Family picture and colour of the signed-in member, so the avatars match the rest of the app. */
  avatarId = computed(() => this.authService.currentUser()?.family?.avatar_id ?? null);
  avatarColour = computed(() => this.authService.currentUser()?.family?.colour ?? '#888888');

  roleName = computed(() => this.authService.currentUser()?.family?.role_name ?? '');

  /** Server build version, shown under the name in the navigation. */
  appVersion = computed(() => this.authService.currentUser()?.version ?? '');

  toggleTheme(): void {
    const next = this.themeService.currentTheme() === 'dark' ? 'light' : 'dark';
    this.themeService.setTheme(next);
    // The choice still applies on this device if the save fails.
    this.authService.saveTheme(next).subscribe({ error: () => undefined });
  }

  logout(): void {
    this.authService.logout().subscribe({
      next: () => this.router.navigate(['/login']),
      error: () => this.router.navigate(['/login']),
    });
  }
}
