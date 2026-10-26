import { TestBed } from '@angular/core/testing';
import { ThemeService } from './theme.service';

describe('ThemeService', () => {
  const systemIsDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  const systemTheme = systemIsDark ? 'dark' : 'light';

  beforeEach(() => {
    localStorage.removeItem('nw-theme');
    TestBed.resetTestingModule();
  });

  it('follows the system theme when nothing is saved on this device', () => {
    const service = TestBed.inject(ThemeService);
    expect(service.preference()).toBe('system');
    expect(service.currentTheme()).toBe(systemTheme);
  });

  it('keeps an explicit dark choice on this device', () => {
    const service = TestBed.inject(ThemeService);
    service.setTheme('dark');
    expect(service.preference()).toBe('dark');
    expect(service.currentTheme()).toBe('dark');
    expect(localStorage.getItem('nw-theme')).toBe('dark');
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
  });

  it('goes back to following the system when the preference is system', () => {
    const service = TestBed.inject(ThemeService);
    service.setTheme('dark');
    service.applyPreference('system');
    expect(localStorage.getItem('nw-theme')).toBeNull();
    expect(service.currentTheme()).toBe(systemTheme);
  });

  it('ignores system passed to setTheme, which only takes light or dark', () => {
    const service = TestBed.inject(ThemeService);
    service.setTheme('light');
    service.setTheme('system');
    expect(service.preference()).toBe('light');
  });
});
