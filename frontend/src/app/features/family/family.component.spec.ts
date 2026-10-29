import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { signal } from '@angular/core';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { mockOf } from '../../testing/mock-of';
import { AuthService, MeResponse } from '../../core/auth/auth.service';
import { AvatarService } from '../../shared/avatar/avatar.service';
import { SessionsService } from '../sessions/sessions.service';
import { FamilyComponent } from './family.component';
import { FamilyService, MemberDetail } from './family.service';

const members: MemberDetail[] = [
    { id: 'm1', user_id: 'sam', display_name: 'Sam', role_id: 'r-parent', role_name: 'Parent', email: 'sam@rivera.home', date_of_birth: null, gender: null, status: 'active', colour: '#6366f1', is_child: false, avatar_id: null },
    { id: 'm2', user_id: 'ava', display_name: 'Ava', role_id: 'r-child', role_name: 'Child', email: null, date_of_birth: null, gender: null, status: 'active', colour: '#f59e0b', is_child: true, avatar_id: 'a1' },
];
const nana: MemberDetail = { ...members[0], id: 'm3', user_id: 'nana', display_name: 'Nana', role_id: 'r-care', role_name: 'Grandparent', email: null, is_child: false };

describe('FamilyComponent', () => {
    let fixture: ComponentFixture<FamilyComponent>;
    let component: FamilyComponent;
    let familySpy: MockedObject<FamilyService>;
    let avatarSpy: MockedObject<AvatarService>;

    function create(): void {
        fixture = TestBed.createComponent(FamilyComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    }

    beforeEach(async () => {
        familySpy = mockOf<FamilyService>({
            listMembers: vi.fn().mockName("FamilyService.listMembers"),
            listRoles: vi.fn().mockName("FamilyService.listRoles"),
            createMember: vi.fn().mockName("FamilyService.createMember"),
            removeMember: vi.fn().mockName("FamilyService.removeMember"),
            getCatalogue: vi.fn().mockName("FamilyService.getCatalogue"),
            createRole: vi.fn().mockName("FamilyService.createRole"),
            updateRolePermissions: vi.fn().mockName("FamilyService.updateRolePermissions"),
            getMemberPermissions: vi.fn().mockName("FamilyService.getMemberPermissions"),
            updateMemberPermissions: vi.fn().mockName("FamilyService.updateMemberPermissions"),
            updateFamilyName: vi.fn().mockName("FamilyService.updateFamilyName")
        });
        familySpy.listMembers.mockReturnValue(of({ members }));
        familySpy.updateFamilyName.mockReturnValue(of({ family_name: 'Rivera family' }));
        familySpy.listRoles.mockReturnValue(of({
            roles: [
                { id: 'r-parent', name: 'Parent', description: '', role_type: 'parent', is_fixed: true, is_built_in: true, permissions: {}, defaults: {}, version: 1 },
                { id: 'r-care', name: 'Grandparent', description: '', role_type: 'child', is_fixed: false, is_built_in: false, permissions: {}, defaults: {}, version: 1 },
                { id: 'r-child', name: 'Child', description: '', role_type: 'child', is_fixed: false, is_built_in: true, permissions: {}, defaults: {}, version: 1 },
            ],
        }));
        familySpy.getCatalogue.mockReturnValue(of({ permissions: [] }));
        familySpy.getMemberPermissions.mockReturnValue(of({ is_parent: false, permissions: {}, version: 1 }));
        familySpy.createMember.mockReturnValue(of(undefined));
        familySpy.removeMember.mockReturnValue(of(undefined));
        avatarSpy = mockOf<AvatarService>({
            url: vi.fn().mockName("AvatarService.url"),
            getSettings: vi.fn().mockName("AvatarService.getSettings"),
            updateSettings: vi.fn().mockName("AvatarService.updateSettings"),
            upload: vi.fn().mockName("AvatarService.upload"),
            remove: vi.fn().mockName("AvatarService.remove")
        });
        avatarSpy.url.mockImplementation((id: string, size: 96 | 256 = 96) => `/api/v1/avatars/${id}?size=${size}`);

        const sessionsSpy = {
            listOwn: vi.fn().mockName("SessionsService.listOwn"),
            listForMember: vi.fn().mockName("SessionsService.listForMember"),
            revokeOwn: vi.fn().mockName("SessionsService.revokeOwn"),
            revokeOthers: vi.fn().mockName("SessionsService.revokeOthers"),
            revokeAllForMember: vi.fn().mockName("SessionsService.revokeAllForMember")
        };
        sessionsSpy.listOwn.mockReturnValue(of({ sessions: [] }));
        sessionsSpy.listForMember.mockReturnValue(of({ sessions: [] }));
        const authSpy = {
            logout: vi.fn().mockName("AuthService.logout"),
            setFamilyName: vi.fn().mockName("AuthService.setFamilyName"),
            can: vi.fn().mockName("AuthService.can"),
            loadPermissions: vi.fn().mockName("AuthService.loadPermissions"),
            currentUser: signal<MeResponse | null | undefined>({
                user: { user_id: 'sam', display_name: 'Sam' } as MeResponse['user'],
                family: { family_name: 'Rivera family' } as MeResponse['family'],
            })
        };
        authSpy.can.mockReturnValue(true);
        authSpy.loadPermissions.mockReturnValue(of(undefined));

        await TestBed.configureTestingModule({
            imports: [FamilyComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: FamilyService, useValue: familySpy },
                { provide: SessionsService, useValue: sessionsSpy },
                { provide: AuthService, useValue: authSpy },
                { provide: AvatarService, useValue: avatarSpy },
            ],
        }).compileComponents();
    });

    it('should create and list members', () => {
        create();
        expect(component).toBeTruthy();
        const cards = fixture.nativeElement.querySelectorAll('[data-testid^="member-"]');
        expect(cards.length).toBe(2);
        expect(familySpy.listRoles).toHaveBeenCalled();
    });

    it('should show an alert when the member list fails to load', () => {
        familySpy.listMembers.mockReturnValue(throwError(() => ({ code: 'forbidden', status: 403 })));
        create();
        const alert = fixture.nativeElement.querySelector('[role="alert"]');
        expect(alert).toBeTruthy();
    });

    it('should reload the member list when retry is clicked after a failed load', () => {
        familySpy.listMembers.mockReturnValue(throwError(() => ({ code: 'network', status: 0 })));
        create();
        const retry = fixture.nativeElement.querySelector('[data-testid="load-error-retry"]') as HTMLButtonElement;
        expect(retry).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="family-loading"]')).toBeNull();

        familySpy.listMembers.mockReturnValue(of({ members }));
        retry.click();
        fixture.detectChanges();

        expect(familySpy.listMembers).toHaveBeenCalledTimes(2);
        expect(fixture.nativeElement.querySelector('[data-testid="load-error-retry"]')).toBeNull();
        expect(fixture.nativeElement.querySelectorAll('[data-testid^="member-"]').length).toBe(2);
    });

    it('should show validation errors and not submit an empty add-member form', () => {
        create();
        component.openAddMember();
        component.submitAddMember();
        fixture.detectChanges();
        expect(familySpy.createMember).not.toHaveBeenCalled();
        expect(fixture.nativeElement.querySelectorAll('.field-error').length).toBeGreaterThan(0);
    });

    it('should create an adult member without a PIN even if one is typed', () => {
        create();
        component.openAddMember();
        component.addMemberForm.setValue({
            role_id: 'r-care',
            display_name: 'Nana',
            user_id: 'nana',
            password: 'secret123',
            colour: '#888888',
            pin: '1234',
            email: '',
            date_of_birth: '',
            gender: '',
        });
        component.submitAddMember();
        expect(familySpy.createMember).toHaveBeenCalledWith({
            role_id: 'r-care',
            user_id: 'nana',
            password: 'secret123',
            display_name: 'Nana',
            colour: '#888888',
        });
    });

    it('should send the email, birth date and gender when they are given', () => {
        create();
        component.openAddMember();
        component.addMemberForm.setValue({
            role_id: 'r-care',
            display_name: 'Nana',
            user_id: 'nana',
            password: 'secret123',
            colour: '#888888',
            pin: '',
            email: 'nana@rivera.home',
            date_of_birth: '1950-04-12',
            gender: 'female',
        });
        component.submitAddMember();
        expect(familySpy.createMember).toHaveBeenCalledWith({
            role_id: 'r-care',
            user_id: 'nana',
            password: 'secret123',
            display_name: 'Nana',
            colour: '#888888',
            email: 'nana@rivera.home',
            date_of_birth: '1950-04-12',
            gender: 'female',
        });
    });

    it('should require an email for a parent on add-member', () => {
        create();
        component.openAddMember();
        component.addMemberForm.controls.role_id.setValue('r-parent');
        component.addMemberForm.patchValue({ display_name: 'Lee', user_id: 'lee', password: 'secret123', colour: '#888888' });
        component.submitAddMember();
        expect(component.addMemberForm.controls.email.hasError('required')).toBe(true);
        expect(familySpy.createMember).not.toHaveBeenCalled();
    });

    it('should leave the email optional for other roles', () => {
        create();
        component.openAddMember();
        component.addMemberForm.controls.role_id.setValue('r-parent');
        component.addMemberForm.controls.role_id.setValue('r-care');
        expect(component.addMemberForm.controls.email.valid).toBe(true);
    });

    it('should send the PIN for a child role', () => {
        create();
        component.openAddMember();
        component.addMemberForm.setValue({
            role_id: 'r-child',
            display_name: 'Mia',
            user_id: 'mia',
            password: 'secret123',
            colour: '#888888',
            pin: '1234',
            email: '',
            date_of_birth: '',
            gender: '',
        });
        component.submitAddMember();
        expect(familySpy.createMember).toHaveBeenCalledWith({
            role_id: 'r-child',
            user_id: 'mia',
            password: 'secret123',
            display_name: 'Mia',
            colour: '#888888',
            pin: '1234',
        });
    });

    it('should show the PIN field only for the Child role', () => {
        create();
        component.openAddMember();
        component.addMemberForm.controls.role_id.setValue('r-care');
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="add-member-dialog"] input[inputmode="numeric"]')).toBeNull();
        component.addMemberForm.controls.role_id.setValue('r-child');
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="add-member-dialog"] input[inputmode="numeric"]')).toBeTruthy();
    });

    it('should show a taken-username message when the user ID already exists', () => {
        familySpy.createMember.mockReturnValue(throwError(() => ({ code: 'conflict', status: 409 })));
        create();
        component.openAddMember();
        component.addMemberForm.setValue({
            role_id: 'r-child',
            display_name: 'Mia',
            user_id: 'mia',
            password: 'secret123',
            colour: '#888888',
            pin: '',
            email: '',
            date_of_birth: '',
            gender: '',
        });
        component.submitAddMember();
        expect(component.addMemberError()).toBe('family.add_member.error.user_id_taken');
    });

    it('should show an email-taken message when the email is already used', () => {
        familySpy.createMember.mockReturnValue(throwError(() => ({ code: 'email_taken', status: 409 })));
        create();
        component.openAddMember();
        component.addMemberForm.setValue({
            role_id: 'r-child',
            display_name: 'Mia',
            user_id: 'mia2',
            password: 'secret123',
            colour: '#888888',
            pin: '',
            email: 'sam@rivera.home',
            date_of_birth: '',
            gender: '',
        });
        component.submitAddMember();
        expect(component.addMemberError()).toBe('family.add_member.error.email_taken');
    });

    it('should open the add-member dialog from the header', () => {
        create();
        fixture.nativeElement.querySelector('[data-testid="add-member"]').click();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="add-member-dialog"]')).toBeTruthy();
    });

    it('should show the Members tab by default and switch to Roles', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="family-tab-members"]').getAttribute('aria-selected')).toBe('true');
        expect(fixture.nativeElement.querySelector('[data-testid="members-grid"]')).toBeTruthy();
        fixture.nativeElement.querySelector('[data-testid="family-tab-roles"]').click();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="members-grid"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('#perm')).toBeTruthy();
    });

    it('should keep the selected tab in the URL', () => {
        create();
        const navigateSpy = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
        component.setTab('roles');
        expect(component.tab()).toBe('roles');
        expect(navigateSpy).toHaveBeenCalledWith([], expect.objectContaining({ queryParams: { tab: 'roles' }, replaceUrl: true }));
    });

    it('should show sessions only on the Sessions tab', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="sessions-grid"]')).toBeNull();
        fixture.nativeElement.querySelector('[data-testid="family-tab-sessions"]').click();
        fixture.detectChanges();
        expect(component.tab()).toBe('sessions');
        expect(fixture.nativeElement.querySelector('[data-testid="family-tab-sessions"]').getAttribute('aria-selected')).toBe('true');
        expect(fixture.nativeElement.querySelector('[data-testid="sessions-grid"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="members-grid"]')).toBeNull();
    });

    it('should not show the removed invite or profile picture controls', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="add-child"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="invite-adult"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="pictures-card"]')).toBeNull();
    });

    it('should link Edit on each card to the member edit page', () => {
        create();
        const link = fixture.nativeElement.querySelector('[data-testid="edit-member-m2"]') as HTMLAnchorElement;
        expect(link.getAttribute('href')).toBe('/family/members/m2');
    });

    it('should show the family name, a pencil and the member count under the title', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="family-name"]').textContent).toBe('Rivera family');
        expect(fixture.nativeElement.querySelector('[data-testid="family-name-edit"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="family-subtitle"]').textContent).toContain('2 members');
    });

    it('should show the email only when the member has one', () => {
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="member-m1"]').textContent).toContain('sam@rivera.home');
        expect(fixture.nativeElement.querySelector('[data-testid="member-m2"]').textContent).not.toContain('@');
    });

    it('should list inactive members and load them with include_inactive', () => {
        const inactive: MemberDetail = { ...members[1], id: 'm4', user_id: 'noah', display_name: 'Noah', status: 'inactive' };
        familySpy.listMembers.mockReturnValue(of({ members: [...members, inactive] }));
        create();
        expect(familySpy.listMembers).toHaveBeenCalledWith(true);
        expect(fixture.nativeElement.querySelector('[data-testid="member-m4"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-member-m4"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="family-name"]').textContent).toBe('Rivera family');
    });

    it('should offer Delete to everyone except the signed-in parent, including another parent', () => {
        const jordan: MemberDetail = { ...members[0], id: 'm5', user_id: 'jordan', display_name: 'Jordan' };
        familySpy.listMembers.mockReturnValue(of({ members: [...members, nana, jordan] }));
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="delete-m2"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="delete-m3"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="delete-m5"]')).toBeTruthy();
        expect(fixture.nativeElement.querySelector('[data-testid="delete-m1"]')).toBeNull();
    });

    it('should hide Delete when the viewer lacks members.manage', () => {
        const authSpy = TestBed.inject(AuthService) as MockedObject<AuthService>;
        authSpy.can.mockReturnValue(false);
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="delete-m2"]')).toBeNull();
    });

    it('should not show Reset password or Edit permissions on member cards', () => {
        familySpy.listMembers.mockReturnValue(of({ members: [...members, nana] }));
        create();
        expect(fixture.nativeElement.querySelector('[data-testid="reset-m2"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="reset-m3"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="edit-permissions-m3"]')).toBeNull();
    });

    it('should open the delete dialog from Delete', () => {
        create();
        fixture.nativeElement.querySelector('[data-testid="delete-m2"]').click();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="delete-dialog"]')).toBeTruthy();
    });

    it('should hard-delete a member on confirm and drop the card', () => {
        create();
        component.startDelete(members[1]);
        component.confirmDelete();
        fixture.detectChanges();
        expect(familySpy.removeMember).toHaveBeenCalledWith('m2');
        expect(fixture.nativeElement.querySelector('[data-testid="member-m2"]')).toBeNull();
        expect(component.deleteMember()).toBeNull();
    });

    it('should keep the card and explain when the server refuses to remove yourself', () => {
        familySpy.removeMember.mockReturnValue(throwError(() => ({ code: 'cannot_remove_self', status: 409 })));
        create();
        component.startDelete(members[1]);
        component.confirmDelete();
        fixture.detectChanges();
        expect(component.deleteError()).toBe('family.delete.error.self');
        expect(fixture.nativeElement.querySelector('[data-testid="member-m2"]')).toBeTruthy();
    });

    it('should explain when the family would lose its last active parent', () => {
        familySpy.removeMember.mockReturnValue(throwError(() => ({ code: 'conflict', status: 409 })));
        create();
        component.startDelete(members[1]);
        component.confirmDelete();
        expect(component.deleteError()).toBe('family.delete.error.last_parent');
    });

    it('should rename the family from the pencil and update the header', () => {
        const authSpy = TestBed.inject(AuthService) as MockedObject<AuthService>;
        familySpy.updateFamilyName.mockReturnValue(of({ family_name: 'Chen family' }));
        create();
        fixture.nativeElement.querySelector('[data-testid="family-name-edit"]').click();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="family-name-input"]')).toBeTruthy();
        component.familyNameForm.setValue({ family_name: '  Chen family  ' });
        fixture.nativeElement.querySelector('[data-testid="family-name-save"]').click();
        fixture.detectChanges();
        expect(familySpy.updateFamilyName).toHaveBeenCalledWith('Chen family');
        expect(authSpy.setFamilyName).toHaveBeenCalledWith('Chen family');
        expect(component.familyNameEditing()).toBe(false);
        expect(fixture.nativeElement.querySelector('[data-testid="family-name-edit"]')).toBeTruthy();
    });

    it('should leave edit mode without saving when the name is unchanged', () => {
        create();
        component.startFamilyNameEdit();
        component.saveFamilyName();
        expect(familySpy.updateFamilyName).not.toHaveBeenCalled();
        expect(component.familyNameEditing()).toBe(false);
    });

    it('should not save a blank family name', () => {
        create();
        component.startFamilyNameEdit();
        component.familyNameForm.setValue({ family_name: '   ' });
        component.saveFamilyName();
        fixture.detectChanges();
        expect(familySpy.updateFamilyName).not.toHaveBeenCalled();
        expect(component.familyNameEditing()).toBe(true);
        expect(fixture.nativeElement.querySelector('[data-testid="family-name-required"]')).toBeTruthy();
    });

    it('should cancel the family name edit with Escape', () => {
        create();
        component.startFamilyNameEdit();
        fixture.detectChanges();
        fixture.nativeElement.querySelector('[data-testid="family-name-input"]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
        fixture.detectChanges();
        expect(component.familyNameEditing()).toBe(false);
        expect(familySpy.updateFamilyName).not.toHaveBeenCalled();
    });

    it('should stay in edit mode and show an error when the rename fails', () => {
        const authSpy = TestBed.inject(AuthService) as MockedObject<AuthService>;
        familySpy.updateFamilyName.mockReturnValue(throwError(() => ({ code: 'validation_failed', status: 400 })));
        create();
        component.startFamilyNameEdit();
        component.familyNameForm.setValue({ family_name: 'Chen family' });
        component.saveFamilyName();
        fixture.detectChanges();
        expect(component.familyNameEditing()).toBe(true);
        expect(component.familyNameSaving()).toBe(false);
        expect(component.familyNameError()).toBe('family.name_edit.error');
        expect(fixture.nativeElement.querySelector('[role="alert"]')).toBeTruthy();
        expect(authSpy.setFamilyName).not.toHaveBeenCalled();
    });

    it('hides the rename pencil from members who lack family.manage', () => {
        const authSpy = TestBed.inject(AuthService) as MockedObject<AuthService>;
        authSpy.can.mockReturnValue(false);
        create();
        expect(authSpy.loadPermissions).toHaveBeenCalled();
        expect(authSpy.can).toHaveBeenCalledWith('family.manage');
        expect(fixture.nativeElement.querySelector('[data-testid="family-name-edit"]')).toBeNull();
        component.startFamilyNameEdit();
        expect(component.familyNameEditing()).toBe(false);
    });
});
