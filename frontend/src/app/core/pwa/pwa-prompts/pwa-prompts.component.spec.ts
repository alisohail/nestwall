import type { Mock } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Subject } from 'rxjs';
import { SwUpdate, VersionEvent } from '@angular/service-worker';
import { getTranslocoTestingModule } from '../../transloco/transloco-testing.module';
import { PwaPromptsComponent } from './pwa-prompts.component';

describe('PwaPromptsComponent', () => {
    let fixture: ComponentFixture<PwaPromptsComponent>;
    let versions: Subject<VersionEvent>;

    function create(swEnabled = true): void {
        versions = new Subject<VersionEvent>();
        TestBed.configureTestingModule({
            imports: [PwaPromptsComponent, getTranslocoTestingModule()],
            providers: [{ provide: SwUpdate, useValue: { isEnabled: swEnabled, versionUpdates: versions.asObservable() } }],
        });
        fixture = TestBed.createComponent(PwaPromptsComponent);
        fixture.detectChanges();
    }

    function fireInstallPrompt(prompt = vi.fn().mockName('prompt').mockResolvedValue(undefined)): Mock {
        const event = new Event('beforeinstallprompt', { cancelable: true }) as Event & {
            prompt: Mock;
        };
        event.prompt = prompt;
        window.dispatchEvent(event);
        fixture.detectChanges();
        return prompt;
    }

    afterEach(() => {
        localStorage.removeItem('nw-pwa-install-dismissed');
    });

    it('should show nothing before the app can be installed or updated', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-install-prompt"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-update-prompt"]')).toBeNull();
    });

    it('should offer install when the browser allows it and run the prompt on Install', async () => {
        create();
        const prompt = fireInstallPrompt();
        const button = fixture.nativeElement.querySelector('[data-testid="pwa-install-accept"]') as HTMLButtonElement;
        expect(button).toBeTruthy();
        button.click();
        await fixture.whenStable();
        expect(prompt).toHaveBeenCalled();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-install-prompt"]')).toBeNull();
    });

    it('should hide install and remember the choice when dismissed', () => {
        create();
        fireInstallPrompt();
        fixture.nativeElement.querySelector('[data-testid="pwa-install-dismiss"]').click();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-install-prompt"]')).toBeNull();
        expect(localStorage.getItem('nw-pwa-install-dismissed')).toBe('1');
    });

    it('should not offer install again after it was dismissed', () => {
        localStorage.setItem('nw-pwa-install-dismissed', '1');
        create();
        fireInstallPrompt();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-install-prompt"]')).toBeNull();
    });

    it('should show the update prompt only when a new version is ready', () => {
        create();
        versions.next({ type: 'VERSION_DETECTED', version: { hash: 'b' } } as VersionEvent);
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-update-prompt"]')).toBeNull();

        versions.next({ type: 'VERSION_READY', currentVersion: { hash: 'a' }, latestVersion: { hash: 'b' } } as VersionEvent);
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-update-prompt"]')).toBeTruthy();
    });

    it('should hide the update prompt when dismissed', () => {
        create();
        versions.next({ type: 'VERSION_READY', currentVersion: { hash: 'a' }, latestVersion: { hash: 'b' } } as VersionEvent);
        fixture.detectChanges();
        fixture.nativeElement.querySelector('[data-testid="pwa-update-dismiss"]').click();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-update-prompt"]')).toBeNull();
    });

    it('should ignore version updates when the service worker is disabled', () => {
        create(false);
        versions.next({ type: 'VERSION_READY', currentVersion: { hash: 'a' }, latestVersion: { hash: 'b' } } as VersionEvent);
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="pwa-update-prompt"]')).toBeNull();
    });
});
