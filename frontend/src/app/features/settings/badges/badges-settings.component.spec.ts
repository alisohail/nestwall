import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { BadgeConfig } from '../../badges/badges.models';
import { BadgesService } from '../../badges/badges.service';
import { BadgesSettingsComponent } from './badges-settings.component';

const config: BadgeConfig = {
  version: 3,
  settings: { enabled: true, show_on_kid_dashboard: true, show_on_wall: false, notify_child: true },
  groups: [
    { group: 'streaks', enabled: true, badges: [{ id: 'b1', key: 's3', enabled: true }] },
    { group: 'points', enabled: true, badges: [{ id: 'b2', key: 'p100', enabled: true }] },
  ],
  custom: [
    { id: 'c1', name: 'Tidy hero', emoji: '⭐', description: null, enabled: true, version: 1, awarded_count: 1 },
  ],
};

describe('BadgesSettingsComponent', () => {
  let fixture: ComponentFixture<BadgesSettingsComponent>;
  let badges: jasmine.SpyObj<BadgesService>;

  function build(): void {
    fixture = TestBed.createComponent(BadgesSettingsComponent);
    fixture.detectChanges();
  }

  function el(selector: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(selector) as HTMLElement | null;
  }

  beforeEach(async () => {
    badges = jasmine.createSpyObj<BadgesService>('BadgesService', [
      'get',
      'saveSettings',
      'setGroup',
      'createCustom',
      'updateCustom',
      'award',
    ]);
    badges.get.and.returnValue(of(config));

    await TestBed.configureTestingModule({
      imports: [BadgesSettingsComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        { provide: BadgesService, useValue: badges },
      ],
    }).compileComponents();
  });

  it('should show the full-page load error and retry the load when loading fails', () => {
    badges.get.and.returnValue(throwError(() => ({ code: 'internal', status: 500 }) as ApiError));
    build();
    expect(el('app-loading')).toBeNull();
    expect(el('app-load-error')).toBeTruthy();

    badges.get.and.returnValue(of(config));
    el('[data-testid="load-error-retry"]')?.click();
    fixture.detectChanges();
    expect(badges.get).toHaveBeenCalledTimes(2);
    expect(el('app-load-error')).toBeNull();
    expect(el('[data-testid="badges-group-streaks"]')).toBeTruthy();
  });

  it('should show the loading state until the config arrives', () => {
    badges.get.and.returnValue(NEVER as Observable<BadgeConfig>);
    build();
    expect(el('[data-testid="badges-loading"]')).toBeTruthy();
  });

  it('should render the groups and the custom badges', () => {
    build();
    expect(el('[data-testid="badges-group-streaks"]')).toBeTruthy();
    expect(el('[data-testid="badges-group-points"]')).toBeTruthy();
    expect(el('[data-testid="badges-custom-c1"]')?.textContent).toContain('Tidy hero');
  });

  it('should send a group switch with the settings version as If-Match', () => {
    badges.setGroup.and.returnValue(of({ ...config, version: 4 }));
    build();

    const input = el('[data-testid="badges-group-switch-streaks"]') as HTMLInputElement;
    input.checked = false;
    input.dispatchEvent(new Event('change'));

    expect(badges.setGroup).toHaveBeenCalledWith('streaks', false, 3);
  });

  it('should offer a reload when the write is stale', () => {
    badges.saveSettings.and.returnValue(throwError(() => ({ code: 'version_conflict', status: 412 }) as ApiError));
    build();

    const input = el('[data-testid="badges-show-wall"]') as HTMLInputElement;
    input.checked = true;
    input.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    expect(input.checked).toBeFalse();
    expect(el('[data-testid="badges-reload"]')).toBeTruthy();
  });

  it('should send the enabled switch value when a custom badge is edited', () => {
    badges.updateCustom.and.returnValue(of(config.custom[0]));
    build();
    fixture.componentInstance.openEdit(config.custom[0]);
    fixture.detectChanges();

    const input = el('[data-testid="badge-enabled"]') as HTMLInputElement;
    input.checked = false;
    input.dispatchEvent(new Event('change'));
    (el('[data-testid="badge-save"]') as HTMLButtonElement).click();

    expect(badges.updateCustom).toHaveBeenCalledWith('c1', jasmine.objectContaining({ enabled: false }), 1);
  });

  it('should save a typed custom emoji such as a skin tone', () => {
    badges.createCustom.and.returnValue(of(config.custom[0]));
    build();
    fixture.componentInstance.openNew();
    fixture.detectChanges();

    const name = el('[data-testid="badge-name"]') as HTMLInputElement;
    name.value = 'Helper';
    name.dispatchEvent(new Event('input'));
    const custom = el('#badge-emoji-custom') as HTMLInputElement;
    custom.value = '👍🏽';
    custom.dispatchEvent(new Event('input'));
    (el('[data-testid="badge-save"]') as HTMLButtonElement).click();

    expect(badges.createCustom).toHaveBeenCalledWith(jasmine.objectContaining({ name: 'Helper', emoji: '👍🏽' }));
  });

  it('should reject more than one emoji in the typed picture', () => {
    build();
    fixture.componentInstance.openNew();
    fixture.detectChanges();

    const name = el('[data-testid="badge-name"]') as HTMLInputElement;
    name.value = 'Helper';
    name.dispatchEvent(new Event('input'));
    const custom = el('#badge-emoji-custom') as HTMLInputElement;
    custom.value = '⭐⭐';
    custom.dispatchEvent(new Event('input'));
    (el('[data-testid="badge-save"]') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(badges.createCustom).not.toHaveBeenCalled();
    expect(fixture.componentInstance.modalError()).toBe('settings.error.emoji_invalid');
  });
});
