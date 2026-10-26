import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { SettingsHubComponent } from './settings-hub.component';

describe('SettingsHubComponent', () => {
  let fixture: ComponentFixture<SettingsHubComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SettingsHubComponent, getTranslocoTestingModule()],
      providers: [provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(SettingsHubComponent);
    fixture.detectChanges();
  });

  it('shows the Settings heading', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('h1')?.textContent?.trim()).toBe('Settings');
  });

  it('does not show a profile card under the heading', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('[data-testid="settings-profile-card"]')).toBeNull();
  });

  it('links to each existing settings section', () => {
    const el: HTMLElement = fixture.nativeElement;
    const paths = ['general', 'templates', 'categories', 'points', 'badges'];
    for (const path of paths) {
      expect(el.querySelector<HTMLAnchorElement>(`[data-testid="settings-section-${path}"]`)?.getAttribute('href')).toBe(
        `/settings/admin/${path}`
      );
    }
  });

  it('lists the built pages first, then the mock sections, with unbuilt pages marked as coming soon', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('.section-label')?.textContent?.trim()).toBe('Configuration');
    const rows = Array.from(el.querySelectorAll('.menu-list .menu-item'));
    expect(rows.map((r) => r.querySelector('.menu-name')?.textContent?.trim())).toEqual([
      'General',
      'Task templates',
      'Task categories',
      'Points and rewards',
      'Badges',
      'Calendars',
      'Notifications',
      'Email (SMTP)',
      'Wall devices',
      'Integrations',
      'Data and backups',
      'Diagnostics',
    ]);
    const soon = rows.filter((r) => r.classList.contains('is-soon'));
    expect(soon.length).toBe(0);
  });

  it('does not offer sign out', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('.logout-btn')).toBeNull();
    expect(el.textContent?.toLowerCase()).not.toContain('sign out');
  });
});
