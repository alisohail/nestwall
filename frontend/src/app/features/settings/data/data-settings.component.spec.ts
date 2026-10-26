import { ComponentFixture, TestBed } from '@angular/core/testing';
import { EMPTY, Observable, of, throwError } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { CalendarService } from '../../calendar/calendar.service';
import { BackupsService } from './backups/backups.service';
import { DataSettingsComponent } from './data-settings.component';

describe('DataSettingsComponent', () => {
  let fixture: ComponentFixture<DataSettingsComponent>;
  let api: jasmine.SpyObj<ApiClient>;
  let calendar: jasmine.SpyObj<CalendarService>;
  let grants: string[];

  function build(): void {
    fixture = TestBed.createComponent(DataSettingsComponent);
    fixture.detectChanges();
  }

  beforeEach(() => {
    grants = ['settings.manage', 'backup.manage', 'backup.restore', 'data.reset'];
    api = jasmine.createSpyObj<ApiClient>('ApiClient', ['download']);
    api.download.and.callFake(() => of(new Blob(['a,b'], { type: 'text/csv' })));
    calendar = jasmine.createSpyObj<CalendarService>('CalendarService', ['exportFamily']);
    calendar.exportFamily.and.returnValue(of(new Blob(['BEGIN:VCALENDAR'], { type: 'text/calendar' })));

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
    spyOn(URL, 'createObjectURL').and.returnValue('blob:test');
    spyOn(URL, 'revokeObjectURL');
    spyOn(HTMLAnchorElement.prototype, 'click');
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

    expect(api.download.calls.allArgs().map((a) => a[0])).toEqual([
      '/api/v1/export/tasks.csv',
      '/api/v1/export/ledger.csv',
      '/api/v1/export/members.csv',
    ]);
    expect(calendar.exportFamily).toHaveBeenCalledTimes(1);
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(4);
  });

  it('shows an error when an export fails', () => {
    api.download.and.returnValue(throwError(() => new Error('network')));
    build();
    fixture.componentInstance.exportAll();
    fixture.detectChanges();

    expect(fixture.componentInstance.exportError()).toBe('settings.data.export.failed');
    expect(fixture.componentInstance.exporting()).toBeFalse();
  });
});
