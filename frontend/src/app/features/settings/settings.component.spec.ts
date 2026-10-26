import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { SettingsComponent } from './settings.component';

describe('SettingsComponent phone trigger', () => {
  let fixture: ComponentFixture<SettingsComponent>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SettingsComponent, getTranslocoTestingModule()],
      providers: [provideRouter([])],
    }).compileComponents();

    fixture = TestBed.createComponent(SettingsComponent);
    fixture.detectChanges();
  });

  it('shows the "Settings" kicker before the current section name, as in the mock', () => {
    const trigger: HTMLElement = fixture.nativeElement.querySelector('.section-trigger');
    const kicker = trigger.querySelector('.trigger-kicker');
    const label = trigger.querySelector('.grow');

    expect(kicker?.textContent?.trim()).toBe('Settings');
    expect(label?.textContent?.trim()).toBe('General');
  });

  it('lists General first, then the built pages and unbuilt pages in hub order', () => {
    const labels = Array.from(fixture.nativeElement.querySelectorAll('.subnav > *'), (row) => {
      const clone = (row as HTMLElement).cloneNode(true) as HTMLElement;
      clone.querySelectorAll('.material-icons').forEach((icon) => icon.remove());
      return clone.textContent?.replace(/\s+/g, ' ').trim();
    });

    expect(labels).toEqual([
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
  });

  it('links every settings page in the sub-nav, with none marked "Coming soon"', () => {
    const soon: HTMLElement[] = Array.from(fixture.nativeElement.querySelectorAll('.subnav .is-soon'));
    const links: HTMLAnchorElement[] = Array.from(fixture.nativeElement.querySelectorAll('.subnav a.subnav-item'));

    expect(soon.length).toBe(0);
    expect(links.map((a) => a.getAttribute('href')).slice(-3)).toEqual([
      '/settings/admin/integrations',
      '/settings/admin/data',
      '/settings/admin/diagnostics',
    ]);
  });

  it('opens the section sheet from the trigger and closes it again', () => {
    const trigger: HTMLButtonElement = fixture.nativeElement.querySelector('.section-trigger');

    trigger.click();
    fixture.detectChanges();
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(fixture.nativeElement.querySelector('.sheet')).not.toBeNull();

    trigger.click();
    fixture.detectChanges();
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
  });
});
