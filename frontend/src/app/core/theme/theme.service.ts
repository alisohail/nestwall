import { Injectable, signal } from '@angular/core';

export interface Theme {
  id: string;
  label: string;
  colorScheme: 'light' | 'dark';
}

export const THEMES: Theme[] = [
  { id: 'light', label: 'Light', colorScheme: 'light' },
  { id: 'dark', label: 'Dark', colorScheme: 'dark' },
];

/** What the user chose. `system` follows the operating system setting. */
export type ThemePreference = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'nw-theme';

@Injectable({
  providedIn: 'root',
})
export class ThemeService {
  preference = signal<ThemePreference>('system');
  currentTheme = signal<string>('light');

  private systemDark = window.matchMedia('(prefers-color-scheme: dark)');

  constructor() {
    // Only an explicit light or dark choice is kept on this device. Older
    // values or no value mean the default, which is system.
    const saved = localStorage.getItem(STORAGE_KEY);
    this.preference.set(saved === 'light' || saved === 'dark' ? saved : 'system');
    this.systemDark.addEventListener('change', () => this.apply());
    this.apply();
  }

  /** Applies an explicit light or dark choice. */
  setTheme(themeId: string): void {
    if (themeId !== 'light' && themeId !== 'dark') return;
    this.applyPreference(themeId);
  }

  /**
   * Sets the preference and applies it. Used on sign-in with the saved server value.
   * It does not save to the server.
   */
  applyPreference(pref: ThemePreference): void {
    this.preference.set(pref);
    if (pref === 'system') {
      localStorage.removeItem(STORAGE_KEY);
    } else {
      localStorage.setItem(STORAGE_KEY, pref);
    }
    this.apply();
  }

  private apply(): void {
    const pref = this.preference();
    const themeId = pref === 'system' ? (this.systemDark.matches ? 'dark' : 'light') : pref;
    const theme = THEMES.find((t) => t.id === themeId);
    if (!theme) return;

    this.currentTheme.set(themeId);
    document.documentElement.setAttribute('data-theme', themeId);
    document.documentElement.style.colorScheme = theme.colorScheme;
  }
}
