import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { EMPTY, Observable, of, throwError } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { CalendarService } from '../../calendar/calendar.service';
import { BackupsService } from './backups/backups.service';
import { DataSettingsComponent } from './data-settings.component';

describe('DataSettingsComponent', () => {
    let fixture: ComponentFixture<DataSettingsComponent>;
    let api: MockedObject<ApiClient>;
    let calendar: MockedObject<CalendarService>;
    let grants: string[];

    function build(): void {
        fixture = TestBed.createComponent(DataSettingsComponent);
        fixture.detectChanges();
    }

    beforeEach(() => {
        grants = ['settings.manage', 'backup.manage', 'backup.restore', 'data.reset'];
        api = mockOf<ApiClient>({
            download: vi.fn().mockName("ApiClient.download")
        });
        api.download.mockImplementation(() => of(new Blob(['a,b'], { type: 'text/csv' })));
        calendar = mockOf<CalendarService>({
            exportFamily: vi.fn().mockName("CalendarService.exportFamily")
        });
        calendar.exportFamily.mockReturnValue(of(new Blob(['BEGIN:VCALENDAR'], { type: 'text/calendar' })));

        TestBed.configureTestingModule({
            imports: [DataSettingsComponent, getTranslocoTestingModule()],
            providers: [
                { provide: ApiClient, useValue: api },
                { provide: BackupsService, useValue: { get: () => EMPTY } },
                { provide: CalendarService, useValue: calendar },
                {
                    provide: AuthService,
                    useValue: {
                        can: (perm: string) => grants.includes(perm),
                        loadPermissions: (): Observable<void> => of(undefined),
                    },
                },
            ],
        });
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test');
        vi.spyOn(URL, 'revokeObjectURL').mockReturnValue(undefined);
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockReturnValue(undefined);
    });

    it('shows a permission banner and no export button without settings.manage', () => {
        grants = [];
        build();

        expect(fixture.nativeElement.querySelector('app-banner')).not.toBeNull();
        expect(fixture.nativeElement.querySelector('button')).toBeNull();
        expect(fixture.nativeElement.querySelector('app-backups-card')).toBeNull();
    });

    it('shows only the export, and no backups card, with settings.manage alone', () => {
        grants = ['settings.manage'];
        build();

        expect(fixture.nativeElement.querySelector('app-backups-card')).toBeNull();
        expect(fixture.nativeElement.textContent).toContain('This is not a backup');
    });

    it('shows the backups card with backup.manage and hides the export without settings.manage', () => {
        grants = ['backup.manage'];
        build();

        expect(fixture.nativeElement.querySelector('app-backups-card')).not.toBeNull();
        expect(fixture.nativeElement.textContent).not.toContain('This is not a backup');
    });

    it('says the export is not a backup', () => {
        build();

        expect(fixture.nativeElement.textContent).toContain('This is not a backup');
    });

    it('downloads the three CSV files and the calendar in order', () => {
        build();
        fixture.componentInstance.exportAll();

        expect(vi.mocked(api.download).mock.calls.map((a) => a[0])).toEqual([
            '/api/v1/export/tasks.csv',
            '/api/v1/export/ledger.csv',
            '/api/v1/export/members.csv',
        ]);
        expect(calendar.exportFamily).toHaveBeenCalledTimes(1);
        expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(4);
    });

    it('shows an error when an export fails', () => {
        api.download.mockReturnValue(throwError(() => new Error('network')));
        build();
        fixture.componentInstance.exportAll();
        fixture.detectChanges();

        expect(fixture.componentInstance.exportError()).toBe('settings.data.export.failed');
        expect(fixture.componentInstance.exporting()).toBe(false);
    });
});
