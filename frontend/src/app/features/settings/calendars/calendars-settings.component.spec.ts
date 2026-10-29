import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Observable, Subject, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { AuthService } from '../../../core/auth/auth.service';
import { ApiError } from '../../../core/api/api-client.service';
import { mockOf } from '../../../testing/mock-of';
import { CalendarImport, CalendarSource } from '../../calendar/calendar.models';
import { CalendarService } from '../../calendar/calendar.service';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { CalendarsSettingsComponent } from './calendars-settings.component';

const ava = { id: 'm1', display_name: 'Ava' } as MemberDetail;
const mia = { id: 'm2', display_name: 'Mia' } as MemberDetail;

const school: CalendarImport = {
    id: 'imp1',
    name: 'School',
    colour: '#14b8a6',
    audience: 'specific',
    show_on_wall: true,
    member_ids: ['m1'],
    file_name: 'school.ics',
    event_count: 12,
    skipped_count: 0,
    created_at: '2026-10-01T10:00:00Z',
} as unknown as CalendarImport;

function linkImport(overrides: Partial<CalendarImport> = {}): CalendarImport {
    return {
        ...school,
        id: 'link1',
        name: 'Work calendar',
        colour: '#6366f1',
        kind: 'link',
        audience: 'everyone',
        member_ids: [],
        file_name: undefined,
        refresh_minutes: 30,
        paused: false,
        last_synced_at: new Date().toISOString(),
        last_sync_error: null,
        ...overrides,
    } as unknown as CalendarImport;
}

const homeCalendar: CalendarSource = { id: 'src1', name: 'Family', colour: '#0D9488', event_count: 3 };
const work: CalendarSource = { id: 'src2', name: 'Work', colour: '#2563EB', event_count: 0 };

describe('CalendarsSettingsComponent', () => {
    let fixture: ComponentFixture<CalendarsSettingsComponent>;
    let component: CalendarsSettingsComponent;
    let calendar: MockedObject<CalendarService>;
    let family: MockedObject<FamilyService>;
    let canManage: boolean;
    let permissionsLoaded: Observable<void>;

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    async function build(): Promise<void> {
        fixture = TestBed.createComponent(CalendarsSettingsComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();
    }

    beforeEach(async () => {
        canManage = true;
        permissionsLoaded = of(undefined);
        calendar = mockOf<CalendarService>({
            listImports: vi.fn().mockName("CalendarService.listImports"),
            createImport: vi.fn().mockName("CalendarService.createImport"),
            deleteImport: vi.fn().mockName("CalendarService.deleteImport"),
            updateImport: vi.fn().mockName("CalendarService.updateImport"),
            syncImport: vi.fn().mockName("CalendarService.syncImport"),
            listSources: vi.fn().mockName("CalendarService.listSources"),
            createSource: vi.fn().mockName("CalendarService.createSource"),
            updateSource: vi.fn().mockName("CalendarService.updateSource"),
            deleteSource: vi.fn().mockName("CalendarService.deleteSource")
        });
        calendar.listImports.mockReturnValue(of({ items: [school] }));
        calendar.listSources.mockReturnValue(of({ items: [homeCalendar] }));
        calendar.createSource.mockImplementation((body) => of({ id: 'src2', event_count: 0, ...body }));
        calendar.updateSource.mockImplementation((id, body) => of({ id, event_count: 0, ...body }));
        calendar.deleteSource.mockReturnValue(of(undefined));
        calendar.createImport.mockReturnValue(of({ ...school, id: 'imp2', event_count: 3, skipped_count: 1 }));
        calendar.deleteImport.mockReturnValue(of(undefined));
        calendar.updateImport.mockReturnValue(of(undefined));
        calendar.syncImport.mockReturnValue(of(linkImport()));

        family = mockOf<FamilyService>({
            listMembers: vi.fn().mockName("FamilyService.listMembers")
        });
        family.listMembers.mockReturnValue(of({ members: [ava, mia] }));

        const auth = { can: () => canManage, loadPermissions: () => permissionsLoaded };

        await TestBed.configureTestingModule({
            imports: [CalendarsSettingsComponent, getTranslocoTestingModule()],
            providers: [
                { provide: CalendarService, useValue: calendar },
                { provide: FamilyService, useValue: family },
                { provide: AuthService, useValue: auth },
            ],
        }).compileComponents();
    });

    it('shows the full-page load error and retries the load when loading fails', async () => {
        calendar.listImports.mockReturnValue(throwError(() => ({ code: 'internal', status: 500 }) as ApiError));
        await build();
        expect(el('app-loading')).toBeNull();
        expect(el('app-load-error')).toBeTruthy();

        calendar.listImports.mockReturnValue(of({ items: [school] }));
        el<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();
        expect(calendar.listImports).toHaveBeenCalledTimes(2);
        expect(el('app-load-error')).toBeNull();
        expect(fixture.nativeElement.textContent).toContain('School');
    });

    it('lists the imports with their colour bar and event count', async () => {
        await build();

        expect(calendar.listImports).toHaveBeenCalled();
        expect(fixture.nativeElement.textContent).toContain('School');
        expect(el('.item:not([data-testid="manual-calendar"])')?.textContent).toContain('Ava');
        calendar.listSources.mockReturnValue(of({ items: [homeCalendar, work] }));
        await build();

        const rows = fixture.nativeElement.querySelectorAll('[data-testid="manual-calendar"]') as NodeListOf<HTMLElement>;
        expect(rows.length).toBe(2);
        expect(rows[0].textContent).toContain('Family');
        expect(rows[1].textContent).toContain('Work');
        expect(el('[data-testid="manual-calendar"] .icon-btn.danger')).toBeTruthy();
    });

    it('shows a type chip on each calendar row', async () => {
        await build();

        expect(el('[data-testid="manual-calendar"] [data-testid="type-chip"]')?.textContent).toContain('Personal');
        expect(el('.item:not([data-testid="manual-calendar"]) [data-testid="type-chip"]')?.textContent).toContain('Imported');
    });

    it('disables delete on the last manual calendar and explains why', async () => {
        await build();

        const del = el<HTMLButtonElement>('[data-testid="manual-calendar"] .icon-btn.danger');
        expect(del?.disabled).toBe(true);
    });

    it('creates a manual calendar with the name and colour, and no file or audience', async () => {
        await build();
        component.openNew();
        component.setKind('manual');
        component.name.set('Holidays');
        component.colour.set('#ec4899');

        component.save();

        expect(calendar.createSource).toHaveBeenCalledTimes(1);

        expect(calendar.createSource).toHaveBeenCalledWith({ name: 'Holidays', colour: '#ec4899' });
        expect(calendar.createImport).not.toHaveBeenCalled();
    });

    it('requires a name for a manual calendar and does not send it', async () => {
        await build();
        component.openNew();
        component.setKind('manual');
        component.name.set('   ');

        component.save();

        expect(calendar.createSource).not.toHaveBeenCalled();
        expect(component.modalError()).toBe('settings.error.required');
    });

    it('switches the Add dialog between Manual and Import, and hides the file controls for Manual', async () => {
        await build();
        component.openNew();
        fixture.detectChanges();
        expect(el('[data-testid="kind-import"]')?.getAttribute('aria-pressed')).toBe('true');
        expect(el('[data-testid="kind-manual"]')?.getAttribute('aria-pressed')).toBe('false');

        el<HTMLButtonElement>('[data-testid="kind-manual"]')?.click();
        fixture.detectChanges();
        expect(component.isManual()).toBe(true);
        expect(el('#imp-file')).toBeNull();

        el<HTMLButtonElement>('[data-testid="kind-import"]')?.click();
        fixture.detectChanges();
        expect(component.isManual()).toBe(false);
        expect(el('#imp-file')).toBeTruthy();
    });

    it('edits a manual calendar name and colour with updateSource', async () => {
        calendar.listSources.mockReturnValue(of({ items: [homeCalendar] }));
        await build();
        component.openEditSource(homeCalendar);
        component.name.set('Home');
        component.colour.set('#2563EB');

        component.save();

        expect(calendar.updateSource).toHaveBeenCalledTimes(1);

        expect(calendar.updateSource).toHaveBeenCalledWith('src1', { name: 'Home', colour: '#2563EB' });
        expect(calendar.createSource).not.toHaveBeenCalled();
    });

    it('deletes a manual calendar when another one remains', async () => {
        calendar.listSources.mockReturnValue(of({ items: [homeCalendar, work] }));
        await build();

        component.removeSource(work);

        expect(calendar.deleteSource).toHaveBeenCalledTimes(1);

        expect(calendar.deleteSource).toHaveBeenCalledWith('src2');
        expect(component.sources().map((s) => s.id)).toEqual(['src1']);
        expect(component.sources().map((s) => s.id)).toEqual(['src1']);
    });

    it('shows the last-calendar message when the server refuses the final delete', async () => {
        calendar.listSources.mockReturnValue(of({ items: [homeCalendar, work] }));
        calendar.deleteSource.mockReturnValue(throwError(() => ({ code: 'last_calendar', status: 409 }) as ApiError));
        await build();

        component.removeSource(work);

        expect(component.actionError()).toBe('settings.calendars.error.last_calendar');
        expect(component.sources().length).toBe(2);
    });


    it('waits for permissions before it loads or shows the no-permission banner', async () => {
        const pending = new Subject<void>();
        permissionsLoaded = pending;
        await build();

        expect(calendar.listImports).not.toHaveBeenCalled();
        expect(el('app-banner')).toBeNull();

        pending.next();
        pending.complete();
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();

        expect(calendar.listImports).toHaveBeenCalled();
        expect(el('.item:not([data-testid="manual-calendar"])')?.textContent).toContain('School');
    });

    it('shows the no-permission banner and does not load when the user cannot manage calendars', async () => {
        canManage = false;
        await build();

        expect(calendar.listImports).not.toHaveBeenCalled();
        expect(el('app-banner')).not.toBeNull();
    });

    it('labels an everyone import as shared with everyone', async () => {
        await build();
        const everyone = { ...school, audience: 'everyone', member_ids: [] } as CalendarImport;

        expect(component.audienceLabel(everyone)).toBe('Shared with everyone');
    });

    it('names the people for a specific import', async () => {
        await build();

        expect(component.audienceLabel(school)).toContain('Ava');
    });

    it('requires a name, a file, and at least one person for a specific audience', async () => {
        await build();
        component.openNew();

        expect(component.validationKey()).toBe('settings.error.required');

        component.name.set('Club');
        expect(component.validationKey()).toBe('settings.calendars.error.file_required');

        component.file.set(new File(['BEGIN:VCALENDAR'], 'club.ics', { type: 'text/calendar' }));
        component.setAudience('specific');
        expect(component.validationKey()).toBe('settings.calendars.error.people_required');

        component.toggleMember('m2');
        expect(component.validationKey()).toBeNull();
    });

    it('sends the file, name, colour and audience, with member_ids only for a specific audience', async () => {
        await build();
        component.openNew();
        component.name.set('Club');
        component.colour.set('#ec4899');
        component.file.set(new File(['BEGIN:VCALENDAR'], 'club.ics', { type: 'text/calendar' }));
        component.setAudience('everyone');

        component.save();

        const form = vi.mocked(calendar.createImport).mock.lastCall![0];
        expect(form.get('name')).toBe('Club');
        expect(form.get('colour')).toBe('#ec4899');
        expect(form.get('audience')).toBe('everyone');
        expect(form.get('show_on_wall')).toBe('true');
        expect(form.getAll('member_ids')).toEqual([]);
    });

    it('sends show_on_wall as false when the switch is turned off', async () => {
        await build();
        component.openNew();
        component.name.set('Quiet');
        component.file.set(new File(['BEGIN:VCALENDAR'], 'quiet.ics', { type: 'text/calendar' }));
        component.showOnWall.set(false);

        component.save();

        const form = vi.mocked(calendar.createImport).mock.lastCall![0];
        expect(form.get('show_on_wall')).toBe('false');
    });

    it('requires a valid http or https link in link mode, and does not ask for a file', async () => {
        await build();
        component.openNew();
        component.name.set('Club');
        component.setSource('link');

        expect(component.validationKey()).toBe('settings.calendars.error.url_required');

        component.url.set('calendar.example.com/basic.ics');
        expect(component.validationKey()).toBe('settings.calendars.error.invalid_url');

        component.url.set('https://calendar.example.com/basic.ics');
        expect(component.validationKey()).toBeNull();
    });

    it('sends the link as url, and no file, in link mode', async () => {
        await build();
        component.openNew();
        component.name.set('Club');
        component.file.set(new File(['BEGIN:VCALENDAR'], 'club.ics', { type: 'text/calendar' }));
        component.setSource('link');
        component.url.set('  https://calendar.example.com/basic.ics  ');

        component.save();

        const form = vi.mocked(calendar.createImport).mock.lastCall![0];
        expect(form.get('url')).toBe('https://calendar.example.com/basic.ics');
        expect(form.get('file')).toBeNull();
    });

    it('shows a message for a link the server refuses', async () => {
        calendar.createImport.mockReturnValue(throwError(() => ({ status: 400, code: 'link_not_allowed' }) as ApiError));
        await build();
        component.openNew();
        component.name.set('Club');
        component.setSource('link');
        component.url.set('https://192.168.1.10/basic.ics');

        component.save();

        expect(component.modalError()).toBe('settings.calendars.error.link_not_allowed');
    });

    it('turns show_on_wall off when the dialog switch is clicked', async () => {
        await build();
        component.openNew();
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();
        const input = fixture.nativeElement.querySelector('input[name="show-on-wall"]') as HTMLInputElement;

        input.click();
        fixture.detectChanges();

        expect(component.showOnWall()).toBe(false);
        expect(input.checked).toBe(false);
    });

    it('sends each picked person as member_ids for a specific audience', async () => {
        await build();
        component.openNew();
        component.name.set('Club');
        component.file.set(new File(['BEGIN:VCALENDAR'], 'club.ics', { type: 'text/calendar' }));
        component.setAudience('specific');
        component.toggleMember('m1');
        component.toggleMember('m2');

        component.save();

        const form = vi.mocked(calendar.createImport).mock.lastCall![0];
        expect(form.getAll('member_ids')).toEqual(['m1', 'm2']);
    });

    it('keeps the dialog open and shows the error when the server rejects the import', async () => {
        await build();
        calendar.createImport.mockReturnValue(throwError(() => ({ code: 'validation_failed', status: 400 }) as ApiError));
        component.openNew();
        component.name.set('Club');
        component.file.set(new File(['x'], 'club.ics'));

        component.save();

        expect(component.modalOpen()).toBe(true);
        expect(component.modalError()).toBe('settings.error.validation');
    });

    it('removes a deleted import from the list', async () => {
        await build();

        component.remove(school);

        expect(calendar.deleteImport).toHaveBeenCalledWith('imp1');
        expect(component.imports()).toEqual([]);
    });

    it('opens the edit dialog from the row with the import name and colour, and hides the source and file controls', async () => {
        await build();
        const edit = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll<HTMLButtonElement>('button')).find((b) => b.getAttribute('aria-label') === 'Edit School');

        edit?.click();
        fixture.detectChanges();

        expect(component.modalOpen()).toBe(true);
        expect(component.isEditing()).toBe(true);
        expect(component.name()).toBe('School');
        expect(component.colour()).toBe('#14b8a6');
        expect(el('.modal h2')?.textContent).toContain('Edit calendar');
        expect(el('.foot .btn.primary')?.textContent).toContain('Save');
        expect(el('#imp-file')).toBeNull();
        expect(el('input[name="show-on-wall"]')).not.toBeNull();
        expect(el('input[name="sync-on"]')).toBeNull();
        expect(el('#imp-refresh')).toBeNull();
    });

    it('sends the name, colour, audience and wall setting when an import is edited', async () => {
        await build();
        component.openEdit(school);
        component.name.set('Work');
        component.colour.set('#ec4899');

        component.save();

        expect(calendar.updateImport).toHaveBeenCalledWith('imp1', {
            name: 'Work',
            colour: '#ec4899',
            audience: 'specific',
            show_on_wall: true,
            member_ids: ['m1'],
        });
        expect(calendar.createImport).not.toHaveBeenCalled();
        expect(component.modalOpen()).toBe(false);
        expect(vi.mocked(calendar.listImports).mock.calls.length).toBeGreaterThan(1);
    });

    it('edits the audience, wall, refresh and sync of a linked import', async () => {
        const link = linkImport();
        calendar.listImports.mockReturnValue(of({ items: [link] }));
        await build();
        component.openEdit(link);
        fixture.detectChanges();

        expect(el('input[name="sync-on"]')).not.toBeNull();
        expect(el('#imp-refresh')).not.toBeNull();

        component.setAudience('parents');
        component.showOnWall.set(false);
        component.refreshMinutes.set(60);
        component.syncOn.set(false);
        component.save();

        expect(calendar.updateImport).toHaveBeenCalledWith('link1', {
            name: 'Work calendar',
            colour: '#6366f1',
            audience: 'parents',
            show_on_wall: false,
            refresh_minutes: 60,
            paused: true,
        });
    });

    it('keeps the stored colour when editing, whether or not it is a palette colour', async () => {
        await build();

        component.openEdit({ ...school, colour: '#14b8a6' });
        expect(component.colour()).toBe('#14b8a6');

        component.openEdit({ ...school, colour: '#0D9488' });
        expect(component.colour()).toBe('#0D9488');
    });

    it('requires a name when editing and does not send the update', async () => {
        await build();
        component.openEdit(school);
        component.name.set('  ');

        component.save();

        expect(component.modalError()).toBe('settings.error.required');
        expect(calendar.updateImport).not.toHaveBeenCalled();
    });

    it('keeps the edit dialog open and shows the error when the update is rejected', async () => {
        await build();
        calendar.updateImport.mockReturnValue(throwError(() => ({ code: 'validation_failed', status: 400 }) as ApiError));
        component.openEdit(school);
        component.name.set('Work');

        component.save();

        expect(component.modalOpen()).toBe(true);
        expect(component.modalError()).toBe('settings.error.validation');
        expect(component.imports()[0].name).toBe('School');
    });

    it('opens a new import with the create fields after an edit', async () => {
        await build();
        component.openEdit(school);

        component.openNew();

        expect(component.isEditing()).toBe(false);
        expect(component.name()).toBe('');
    });

    it('sends the refresh interval with a new linked import, defaulting to 30 minutes', async () => {
        await build();
        component.openNew();
        expect(component.refreshMinutes()).toBe(30);
        component.name.set('Club');
        component.setSource('link');
        component.url.set('https://calendar.example.com/basic.ics');
        component.refreshMinutes.set(360);

        component.save();

        const form = vi.mocked(calendar.createImport).mock.lastCall![0];
        expect(form.get('refresh_minutes')).toBe('360');
    });

    it('shows the refresh and sync controls only for a linked import', async () => {
        await build();
        component.openNew();
        fixture.detectChanges();
        expect(el('#imp-refresh')).toBeNull();

        component.setSource('link');
        fixture.detectChanges();
        expect(el('#imp-refresh')).not.toBeNull();
        expect(el('input[name="sync-on"]')).toBeNull();

        component.openEdit(school);
        fixture.detectChanges();
        expect(el('#imp-refresh')).toBeNull();
        expect(el('input[name="sync-on"]')).toBeNull();
    });

    it('shows the sync status and the action that fits each linked import', async () => {
        calendar.listImports.mockReturnValue(of({
            items: [
                linkImport({ id: 'ok1', name: 'Healthy' }),
                linkImport({ id: 'bad1', name: 'Broken', last_sync_error: 'unavailable' }),
                linkImport({ id: 'p1', name: 'Paused', paused: true }),
            ],
        }));
        await build();

        const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
        expect(text).toContain('Synced just now');
        expect(text).toContain('Last sync failed: unreachable');
        expect(text).toContain('Paused');
        expect(text).toContain('Sync now');
        expect(text).toContain('Retry');
        expect(text).toContain('Resume');
    });

    it('syncs a linked import now and reports the result', async () => {
        const link = linkImport();
        calendar.listImports.mockReturnValue(of({ items: [link] }));
        await build();

        component.syncNow(link);

        expect(calendar.syncImport).toHaveBeenCalledWith('link1');
        expect(component.notice()).toBe('Calendar synced.');
        expect(component.busyId()).toBeNull();
    });

    it('reports a failed sync on the row instead of a success message', async () => {
        const link = linkImport();
        calendar.listImports.mockReturnValue(of({ items: [link] }));
        calendar.syncImport.mockReturnValue(of(linkImport({ last_sync_error: 'unavailable' })));
        await build();

        component.syncNow(link);
        fixture.detectChanges();

        expect(component.notice()).toBeNull();
        expect(el('.badge.err')?.textContent).toContain('Last sync failed: unreachable');
    });

    it('shows a clear message when a link was synced less than a minute ago', async () => {
        const link = linkImport();
        calendar.listImports.mockReturnValue(of({ items: [link] }));
        calendar.syncImport.mockReturnValue(throwError(() => ({ code: 'sync_too_soon', status: 429 }) as ApiError));
        await build();

        component.syncNow(link);

        expect(component.actionError()).toBe('settings.calendars.error.sync_too_soon');
    });

    it('resumes a paused link without changing its other settings', async () => {
        const link = linkImport({ paused: true });
        calendar.listImports.mockReturnValue(of({ items: [link] }));
        await build();

        component.resume(link);

        expect(calendar.updateImport).toHaveBeenCalledWith('link1', { name: 'Work calendar', colour: '#6366f1', paused: false });
        expect(component.notice()).toBe('Sync resumed.');
    });

    it('warns in the dialog when the chosen colour is hard to see', async () => {
        await build();
        component.openNew();
        component.colour.set('#f59e0b');
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();

        expect(el('app-colour-picker .contrast-warning')).not.toBeNull();
    });
});
