import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, ParamMap, Router, convertToParamMap, provideRouter } from '@angular/router';
import { signal } from '@angular/core';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { AuthService, MeResponse } from '../../../core/auth/auth.service';
import { AvatarService } from '../../../shared/avatar/avatar.service';
import { FamilyService, MemberDetail, RoleView } from '../family.service';
import { MemberEditComponent } from './member-edit.component';

const roles: RoleView[] = [
    { id: 'r-parent', name: 'Parent', description: '', role_type: 'parent', is_fixed: true, is_built_in: true, permissions: {}, defaults: {}, version: 1 },
    { id: 'r-care', name: 'Grandparent', description: '', role_type: 'child', is_fixed: false, is_built_in: false, permissions: {}, defaults: {}, version: 1 },
    { id: 'r-child', name: 'Child', description: '', role_type: 'child', is_fixed: false, is_built_in: true, permissions: {}, defaults: {}, version: 1 },
];

const sam: MemberDetail = {
    id: 'm1',
    user_id: 'sam',
    display_name: 'Sam',
    role_id: 'r-parent',
    role_name: 'Parent',
    email: 'sam@rivera.home',
    date_of_birth: '1980-02-03',
    gender: 'male',
    status: 'active',
    colour: '#6366f1',
    is_child: false,
    avatar_id: null,
};
const ava: MemberDetail = {
    id: 'm2',
    user_id: 'ava',
    display_name: 'Ava',
    role_id: 'r-child',
    role_name: 'Child',
    email: null,
    date_of_birth: null,
    gender: null,
    status: 'active',
    colour: '#f59e0b',
    is_child: true,
    avatar_id: 'a1',
};
const nana: MemberDetail = {
    ...sam,
    id: 'm3',
    user_id: 'nana',
    display_name: 'Nana',
    role_id: 'r-care',
    role_name: 'Grandparent',
    email: 'nana@rivera.home',
    date_of_birth: '1950-04-12',
    gender: 'female',
    is_child: false,
};

describe('MemberEditComponent', () => {
    let fixture: ComponentFixture<MemberEditComponent>;
    let component: MemberEditComponent;
    let familySpy: MockedObject<FamilyService>;
    let authSpy: MockedObject<AuthService>;
    let routeStub: {
        snapshot: {
            paramMap: ParamMap;
        };
    };

    function create(): void {
        fixture = TestBed.createComponent(MemberEditComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    }

    beforeEach(async () => {
        familySpy = mockOf<FamilyService>({
            listMembers: vi.fn().mockName("FamilyService.listMembers"),
            listRoles: vi.fn().mockName("FamilyService.listRoles"),
            updateMember: vi.fn().mockName("FamilyService.updateMember")
        });
        familySpy.listMembers.mockReturnValue(of({ members: [sam, ava, nana] }));
        familySpy.listRoles.mockReturnValue(of({ roles }));
        familySpy.updateMember.mockReturnValue(of(undefined));

        authSpy = mockOf<AuthService>({
            can: vi.fn().mockName("AuthService.can"),
            loadPermissions: vi.fn().mockName("AuthService.loadPermissions"),
            currentUser: signal<MeResponse | null | undefined>({
                user: { user_id: 'sam', display_name: 'Sam' } as MeResponse['user'],
                family: { family_name: 'Rivera family' } as MeResponse['family'],
            })
        });
        authSpy.can.mockReturnValue(true);
        authSpy.loadPermissions.mockReturnValue(of(undefined));

        routeStub = { snapshot: { paramMap: convertToParamMap({ id: 'm1' }) } };
        const avatarSpy = {
            url: vi.fn().mockName("AvatarService.url"),
            getSettings: vi.fn().mockName("AvatarService.getSettings"),
            updateSettings: vi.fn().mockName("AvatarService.updateSettings"),
            upload: vi.fn().mockName("AvatarService.upload"),
            remove: vi.fn().mockName("AvatarService.remove")
        };
        avatarSpy.url.mockImplementation((id: string, size: 96 | 256 = 96) => `/api/v1/avatars/${id}?size=${size}`);

        await TestBed.configureTestingModule({
            imports: [MemberEditComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: ActivatedRoute, useValue: routeStub },
                { provide: FamilyService, useValue: familySpy },
                { provide: AuthService, useValue: authSpy },
                { provide: AvatarService, useValue: avatarSpy },
            ],
        }).compileComponents();
    });

    it('should load the member into the form', () => {
        create();
        expect(component.member()?.id).toBe('m1');
        expect(component.form.controls.display_name.value).toBe('Sam');
        expect(component.form.controls.email.value).toBe('sam@rivera.home');
        expect(component.form.controls.date_of_birth.value).toBe('1980-02-03');
        expect(component.form.controls.gender.value).toBe('male');
    });

    it('should lock the role for a parent', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-role-locked"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-role"]')).toBeNull();
    });

    it('should show the role select for a non-parent and list only non-fixed roles', () => {
        routeStub.snapshot = { paramMap: convertToParamMap({ id: 'm3' }) };
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-role"]')).toBeTruthy();
        expect(component.assignableRoles().map((r) => r.id)).toEqual(['r-care', 'r-child']);
    });

    it('should show the not-found state for an unknown member id', () => {
        routeStub.snapshot = { paramMap: convertToParamMap({ id: 'nobody' }) };
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="member-edit-missing"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="member-edit-form"]')).toBeNull();
    });

    it('should show a load error when the family cannot be read', () => {
        familySpy.listMembers.mockReturnValue(throwError(() => ({ code: 'forbidden', status: 403 })));
        create();
        expect(component.loadError()?.status).toBe(403);
        expect(fixture.nativeElement.querySelector('[data-testid="member-edit-error"] [data-testid="load-error-retry"]')).toBeTruthy();
        familySpy.listMembers.mockReturnValue(of({ members: [] }));
        (fixture.nativeElement.querySelector('[data-testid="load-error-retry"]') as HTMLButtonElement).click();
        expect(familySpy.listMembers).toHaveBeenCalledTimes(2);
        expect(component.loadError()).toBeNull();
    });

    it('should keep the Save button disabled until something changes', () => {
        create();
        const save = fixture.nativeElement.querySelector('[data-testid="member-edit-save"]') as HTMLButtonElement;
        expect(save.disabled).toBe(true);
        component.form.controls.display_name.setValue('Samuel');
        fixture.detectChanges();
        expect(save.disabled).toBe(false);
    });

    it('should send only the changed fields', () => {
        create();
        component.form.controls.display_name.setValue('Samuel');
        expect(component.buildRequest()).toEqual({ display_name: 'Samuel' });
    });

    it('should send the date of birth and gender when they change', () => {
        create();
        component.form.patchValue({ date_of_birth: '1981-01-01', gender: 'prefer_not_to_say' });
        expect(component.buildRequest()).toEqual({ date_of_birth: '1981-01-01', gender: 'prefer_not_to_say' });
    });

    it('should clear the email for a non-parent when it is emptied', () => {
        routeStub.snapshot = { paramMap: convertToParamMap({ id: 'm3' }) };
        create();
        component.form.controls.email.setValue('');
        expect(component.buildRequest()).toEqual({ email: '' });
    });

    it('should require an email for a parent and not save without one', () => {
        create();
        component.form.controls.email.setValue('');
        expect(component.form.controls.email.hasError('required')).toBe(true);
        component.save();
        expect(familySpy.updateMember).not.toHaveBeenCalled();
    });

    it('should reject a future date of birth', () => {
        create();
        component.form.controls.date_of_birth.setValue('2999-01-01');
        expect(component.form.controls.date_of_birth.hasError('range')).toBe(true);
        component.save();
        expect(familySpy.updateMember).not.toHaveBeenCalled();
    });

    it('should send a new PIN only for a child', () => {
        routeStub.snapshot = { paramMap: convertToParamMap({ id: 'm2' }) };
        create();
        component.form.controls.pin.setValue('1234');
        expect(component.buildRequest()).toEqual({ pin: '1234' });
    });

    it('should not offer a PIN or a password for the signed-in parent, only change password', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-pin"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-password"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="member-edit-own-password"]')).toBeTruthy();
    });

    it('should let an admin set another member password without a reset', () => {
        routeStub.snapshot = { paramMap: convertToParamMap({ id: 'm3' }) };
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-password"]')).toBeTruthy();
        component.form.controls.password.setValue('NewSecret1');
        expect(component.buildRequest()).toEqual({ password: 'NewSecret1' });
    });

    it('should hide the active switch for the signed-in user', () => {
        create();
        expect(component.isSelf()).toBe(true);
        expect(fixture.nativeElement.querySelector('[data-testid="edit-active"]')).toBeNull();
    });

    it('should deactivate another member and send the status', () => {
        routeStub.snapshot = { paramMap: convertToParamMap({ id: 'm3' }) };
        create();
        component.form.controls.active.setValue(false);
        expect(component.buildRequest()).toEqual({ status: 'inactive' });
    });

    it('should save only the changes and go back to the Members tab', async () => {
        const routerSpy = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
        create();
        component.form.controls.display_name.setValue('Samuel');
        component.save();
        await fixture.whenStable();
        expect(familySpy.updateMember).toHaveBeenCalledWith('m1', { display_name: 'Samuel' });
        expect(routerSpy).toHaveBeenCalledWith(['/family'], { queryParams: { tab: 'members' } });
    });

    it('should not ask about unsaved changes after a successful save', async () => {
        vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
        create();
        component.form.controls.display_name.setValue('Samuel');
        expect(component.hasUnsavedChanges()).toBe(true);
        component.save();
        await fixture.whenStable();
        expect(component.hasUnsavedChanges()).toBe(false);
    });

    it('should keep the page and show the mapped error when the save is rejected', () => {
        familySpy.updateMember.mockReturnValue(throwError(() => ({ code: 'user_id_taken', status: 409 })));
        create();
        component.form.controls.display_name.setValue('Samuel');
        component.save();
        fixture.detectChanges();
        expect(component.saveError()).toBe('family.edit.error.user_id_taken');
        expect(fixture.nativeElement.querySelector('[data-testid="member-edit-save-error"]')).toBeTruthy();
    });

    it('should map a duplicate email to its own message', () => {
        familySpy.updateMember.mockReturnValue(throwError(() => ({ code: 'email_taken', status: 409 })));
        create();
        component.form.controls.display_name.setValue('Samuel');
        component.save();
        expect(component.saveError()).toBe('family.edit.error.email_taken');
    });

    it('should show a read-only page with no Save button without members.manage', () => {
        authSpy.can.mockReturnValue(false);
        create();
        expect(component.form.disabled).toBe(true);
        expect(fixture.nativeElement.querySelector('[data-testid="member-edit-read-only"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="member-edit-save"]')).toBeNull();
    });

    it('should load permissions before deciding whether the form is editable', () => {
        create();
        expect(authSpy.loadPermissions).toHaveBeenCalled();
        expect(component.form.enabled).toBe(true);
    });

    it('should link the back button to Family with the Members tab', () => {
        create();
        const back = fixture.nativeElement.querySelector('[data-testid="member-edit-back"]') as HTMLAnchorElement;
        expect(back.getAttribute('href')).toBe('/family?tab=members');
    });
});
