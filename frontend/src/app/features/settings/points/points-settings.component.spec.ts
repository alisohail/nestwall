import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { NEVER, Observable, of, throwError } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { RewardSettings } from '../../rewards/rewards.models';
import { RewardsService } from '../../rewards/rewards.service';
import { ShareSummary } from '../../sharing/sharing.models';
import { SharingService } from '../../sharing/sharing.service';
import { PointsSettingsComponent } from './points-settings.component';

const summary = {
    enabled: false,
    min_per_share: 1,
    max_per_share: 10,
    daily_limit: 20,
    min_balance: 0,
    approval_mode: 'never',
    approval_above: 0,
    notify_recipient: true,
    notify_parents: false,
    version: 1,
    balance: 0,
    shared_today: 0,
    daily_left: 20,
    notes: [],
    recipients: [],
} as unknown as ShareSummary;

describe('PointsSettingsComponent', () => {
    let fixture: ComponentFixture<PointsSettingsComponent>;
    let sharing: MockedObject<SharingService>;
    let rewards: MockedObject<RewardsService>;

    function build(): void {
        fixture = TestBed.createComponent(PointsSettingsComponent);
        fixture.detectChanges();
    }

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    beforeEach(async () => {
        sharing = mockOf<SharingService>({
            summary: vi.fn().mockName("SharingService.summary"),
            saveSettings: vi.fn().mockName("SharingService.saveSettings")
        });
        sharing.summary.mockReturnValue(of(summary));
        rewards = mockOf<RewardsService>({
            settings: vi.fn().mockName("RewardsService.settings"),
            saveSettings: vi.fn().mockName("RewardsService.saveSettings")
        });
        rewards.settings.mockReturnValue(of({ approval_required_default: false }));

        await TestBed.configureTestingModule({
            imports: [PointsSettingsComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: SharingService, useValue: sharing },
                { provide: RewardsService, useValue: rewards },
            ],
        }).compileComponents();
    });

    it('should show the spending switch in its loading state until the default arrives', () => {
        rewards.settings.mockReturnValue(NEVER as Observable<RewardSettings>);
        build();
        expect(el('[data-testid="reward-approval-default"]')).toBeNull();
        expect(el('#spend-settings-title')).toBeNull();
    });

    it('should show the saved family default as on', async () => {
        rewards.settings.mockReturnValue(of({ approval_required_default: true }));
        build();
        await fixture.whenStable();
        fixture.detectChanges();
        expect(el<HTMLInputElement>('[data-testid="reward-approval-default"]')?.checked).toBe(true);
    });

    it('should save the family default and show the saved label', () => {
        rewards.saveSettings.mockReturnValue(of({ approval_required_default: true }));
        build();
        el<HTMLInputElement>('[data-testid="reward-approval-default"]')?.click();
        fixture.detectChanges();
        el<HTMLButtonElement>('[data-testid="reward-approval-save"]')?.click();
        fixture.detectChanges();

        expect(rewards.saveSettings).toHaveBeenCalledWith({ approval_required_default: true });
        expect(el('.foot .saved')).toBeTruthy();
    });

    it('should show an error and keep the switch when the save fails', () => {
        rewards.saveSettings.mockReturnValue(throwError(() => ({ code: 'internal_error', status: 500 }) as ApiError));
        build();
        el<HTMLInputElement>('[data-testid="reward-approval-default"]')?.click();
        fixture.detectChanges();
        el<HTMLButtonElement>('[data-testid="reward-approval-save"]')?.click();
        fixture.detectChanges();

        expect(el('.foot .saved')).toBeNull();
        expect(el('.foot app-banner')).toBeTruthy();
    });

    it('should keep the sharing rules card alongside the spending card', () => {
        build();
        expect(el('[data-testid="share-enabled"]')).toBeTruthy();
        expect(el('[data-testid="share-save"]')).toBeTruthy();
    });
});
