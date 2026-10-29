import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Subject, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { AuthService } from '../../../core/auth/auth.service';
import { ApiError } from '../../../core/api/api-client.service';
import { WallDeviceCreated, WallDeviceView, WallDevicesService } from './wall-devices.service';
import { WallDevicesSettingsComponent } from './wall-devices-settings.component';
import { mockOf } from '../../../testing/mock-of';

function device(overrides: Partial<WallDeviceView> = {}): WallDeviceView {
    return {
        id: 'd1',
        name: 'Kitchen tablet',
        status: 'online',
        paired_at: '2026-10-03T10:00:00Z',
        last_seen_at: new Date().toISOString(),
        ...overrides,
    };
}

describe('WallDevicesSettingsComponent', () => {
    let fixture: ComponentFixture<WallDevicesSettingsComponent>;
    let component: WallDevicesSettingsComponent;
    let api: MockedObject<WallDevicesService>;
    let canManage: boolean;

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    function build(): void {
        fixture = TestBed.createComponent(WallDevicesSettingsComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    }

    beforeEach(() => {
        canManage = true;
        api = mockOf<WallDevicesService>({
            list: vi.fn().mockName("WallDevicesService.list"),
            create: vi.fn().mockName("WallDevicesService.create"),
            update: vi.fn().mockName("WallDevicesService.update"),
            revoke: vi.fn().mockName("WallDevicesService.revoke")
        });
        api.list.mockReturnValue(of([device()]));
        api.update.mockImplementation((id, body) => of(device({ id, name: body.name ?? 'Kitchen tablet' })));
        api.revoke.mockReturnValue(of(undefined));

        TestBed.configureTestingModule({
            imports: [WallDevicesSettingsComponent, getTranslocoTestingModule()],
            providers: [
                { provide: WallDevicesService, useValue: api },
                {
                    provide: AuthService,
                    useValue: {
                        isParent: (): boolean => canManage,
                    },
                },
            ],
        });
    });

    it('shows a permission banner and does not load devices for a non-parent', () => {
        canManage = false;
        build();

        expect(el('app-banner')).not.toBeNull();
        expect(api.list).not.toHaveBeenCalled();
    });

    it('shows the load error screen and retry reloads the devices', () => {
        api.list.mockReturnValue(throwError(() => ({ code: 'network', status: 0 }) as ApiError));
        build();

        expect(el('app-loading')).toBeNull();
        expect(el('app-load-error')).not.toBeNull();

        api.list.mockReturnValue(of([device()]));
        el<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
        fixture.detectChanges();

        expect(api.list).toHaveBeenCalledTimes(2);
        expect(el('app-load-error')).toBeNull();
    });

    it('lists devices without display options or an inline exit code', () => {
        api.list.mockReturnValue(of([device(), device({ id: 'd2', name: 'Hallway', status: 'offline' })]));
        build();

        expect(fixture.nativeElement.querySelectorAll('.item').length).toBe(2);
        expect(fixture.nativeElement.querySelectorAll('input[role="switch"]').length).toBe(0);
        expect(el('.masked')).toBeNull();
        expect(el('.exit-input')).toBeNull();
    });

    it('opens the configure dialog with the current name and a blank exit code', async () => {
        build();
        component.openEdit(device());
        fixture.detectChanges();
        await new Promise<void>((resolve) => setTimeout(resolve));
        fixture.detectChanges();

        expect(el<HTMLInputElement>('#wall-edit-name')?.value).toBe('Kitchen tablet');
        expect(el<HTMLInputElement>('#wall-edit-exit')?.value).toBe('');
    });

    it('rejects an exit code that is not 4 to 6 digits without calling the API', () => {
        build();
        component.openEdit(device());
        component.editExitCode.set('12a');

        component.saveEdit();

        expect(api.update).not.toHaveBeenCalled();
        expect(component.editError()).toBe('settings.wall.error.exit_code');
        expect(component.editing()).not.toBeNull();
    });

    it('asks for a name in the configure dialog', () => {
        build();
        component.openEdit(device());
        component.editName.set('   ');

        component.saveEdit();

        expect(api.update).not.toHaveBeenCalled();
        expect(component.editError()).toBe('settings.error.required');
    });

    it('saves a changed name and exit code together and closes the dialog', () => {
        build();
        component.openEdit(device());
        component.editName.set('Hall tablet');
        component.editExitCode.set('123456');

        component.saveEdit();

        expect(api.update).toHaveBeenCalledWith('d1', { name: 'Hall tablet', exit_code: '123456' });
        expect(component.editing()).toBeNull();
        expect(component.notice()).toBeTruthy();
    });

    it('sends only the name when the exit code is left blank', () => {
        build();
        component.openEdit(device());
        component.editName.set('Hall tablet');

        component.saveEdit();

        expect(api.update).toHaveBeenCalledWith('d1', { name: 'Hall tablet' });
    });

    it('closes without calling the API when nothing changed', () => {
        build();
        component.openEdit(device());

        component.saveEdit();

        expect(api.update).not.toHaveBeenCalled();
        expect(component.editing()).toBeNull();
    });

    it('asks for a name before creating a key', () => {
        build();
        component.openNew();
        component.name.set('   ');
        component.newExitCode.set('1234');

        component.create();

        expect(api.create).not.toHaveBeenCalled();
        expect(component.modalError()).toBe('settings.error.required');
    });

    it('shows the pairing key with a countdown after it is created', () => {
        const created: WallDeviceCreated = {
            id: 'd3',
            name: 'Living room',
            pairing_key: 'K7M4-9QXD',
            expires_at: new Date(Date.now() + 600000).toISOString(),
        };
        api.create.mockReturnValue(of(created));
        build();
        component.openNew();
        component.name.set('Living room');
        component.newExitCode.set('1234');

        component.create();
        fixture.detectChanges();

        expect(api.create).toHaveBeenCalledWith({ name: 'Living room', exit_code: '1234' });
        expect(component.created()?.pairing_key).toBe('K7M4-9QXD');
        expect(component.secondsLeft()).toBeGreaterThan(590);
        expect(el('.pair-key')?.textContent?.trim()).toBe('K7M4-9QXD');
        component.closeModal();
    });

    it('removes a revoked device from the list', () => {
        api.list.mockReturnValue(of([device(), device({ id: 'd2', name: 'Hallway' })]));
        build();

        component.revoke(device({ id: 'd2', name: 'Hallway' }));

        expect(api.revoke).toHaveBeenCalledWith('d2');
        expect(component.devices().map((d) => d.id)).toEqual(['d1']);
    });

    it('shows an error when the list cannot load', () => {
        const pending = new Subject<WallDeviceView[]>();
        api.list.mockReturnValue(pending.asObservable());
        build();
        pending.error({ code: 'network', status: 0 } as ApiError);

        expect(component.loadState()).toBe('error');
        expect(component.loadError()).toBe('settings.error.network');
    });
});
