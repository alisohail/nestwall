import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { FamilyService, MemberDetail, PermissionEntry, RoleView } from '../family.service';
import { PermissionsEditorComponent } from './permissions-editor.component';

const catalogue: PermissionEntry[] = [
  { code: 'tasks.view', description: '', levels: ['denied', 'allowed', 'view'], audience: 'any' },
  { code: 'tasks.complete', description: '', levels: ['denied', 'allowed', 'request', 'full'], audience: 'any' },
  { code: 'members.manage', description: '', levels: ['denied', 'allowed', 'full'], audience: 'co_parent' },
  { code: 'profile.avatar.change', description: '', levels: ['denied', 'allowed'], audience: 'any' },
];

const roles: RoleView[] = [
  { id: 'r-parent', name: 'Parent', description: '', role_type: 'parent', is_fixed: true, is_built_in: true, permissions: { 'tasks.view': 'full' }, defaults: { 'tasks.view': 'full' }, version: 1 },
  { id: 'r-care', name: 'Grandparent', description: '', role_type: 'child', is_fixed: false, is_built_in: false, permissions: { 'tasks.view': 'view' }, defaults: { 'tasks.view': 'view' }, version: 1 },
  { id: 'r-child', name: 'Child', description: '', role_type: 'child', is_fixed: false, is_built_in: true, permissions: { 'tasks.complete': 'allowed' }, defaults: { 'tasks.complete': 'allowed' }, version: 1 },
];

const members: MemberDetail[] = [
  { id: 'm1', user_id: 'sam', display_name: 'Sam', role_id: 'r-parent', role_name: 'Parent',   email: 'sam@rivera.home', date_of_birth: null, gender: null, status: 'active', colour: '#6366f1', is_child: false, avatar_id: null },
    { id: 'm2', user_id: 'ava', display_name: 'Ava', role_id: 'r-child', role_name: 'Child', email: null, date_of_birth: null, gender: null, status: 'active', colour: '#f59e0b', is_child: true, avatar_id: null },
];

describe('PermissionsEditorComponent', () => {
  let fixture: ComponentFixture<PermissionsEditorComponent>;
  let familySpy: jasmine.SpyObj<FamilyService>;

  function create(): void {
    fixture = TestBed.createComponent(PermissionsEditorComponent);
    fixture.detectChanges();
  }

  function choose(testId: string, value: string): void {
    const select = fixture.nativeElement.querySelector(`[data-testid="${testId}"]`) as HTMLSelectElement;
    select.value = value;
    select.dispatchEvent(new Event('change'));
    fixture.detectChanges();
  }

  function button(testId: string): HTMLButtonElement {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`) as HTMLButtonElement;
  }

  beforeEach(async () => {
    familySpy = jasmine.createSpyObj<FamilyService>('FamilyService', [
      'listMembers',
      'listRoles',
      'getCatalogue',
      'createRole',
      'updateRolePermissions',
      'getMemberPermissions',
      'updateMemberPermissions',
      'deleteRole',
    ]);
    familySpy.getCatalogue.and.returnValue(of({ permissions: catalogue }));
    familySpy.listRoles.and.returnValue(of({ roles }));
    familySpy.listMembers.and.returnValue(of({ members }));
    familySpy.updateRolePermissions.and.returnValue(of({ version: 2 }));
    familySpy.updateMemberPermissions.and.returnValue(of({ version: 2 }));
    familySpy.getMemberPermissions.and.returnValue(of({ is_parent: false, permissions: {}, version: 1 }));
    familySpy.deleteRole.and.returnValue(of(undefined));

    await TestBed.configureTestingModule({
      imports: [PermissionsEditorComponent, getTranslocoTestingModule()],
      providers: [
        { provide: FamilyService, useValue: familySpy },
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
  });

  it('shows one row per catalogue code and locks the Parent column', () => {
    create();
    expect(fixture.nativeElement.querySelectorAll('[data-testid^="perm-row-"]').length).toBe(3);
    // Parent column (3 rows) plus members.manage for the two child roles
    expect(fixture.nativeElement.querySelectorAll('.locked').length).toBe(5);
    expect(fixture.nativeElement.querySelector('[data-testid="cell-r-care-tasks.view"]')).toBeTruthy();
  });

  it('hides the profile picture permission from the matrix', () => {
    create();
    expect(fixture.nativeElement.querySelector('[data-testid="perm-row-profile.avatar.change"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="cell-r-care-profile.avatar.change"]')).toBeNull();
    expect(fixture.nativeElement.textContent).not.toContain('profile.avatar.change');
  });

  it('locks co-parent codes for child roles', () => {
    create();
    expect(fixture.nativeElement.querySelector('[data-testid="cell-r-care-members.manage"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="cell-r-child-members.manage"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="cell-r-care-tasks.complete"]')).toBeTruthy();
  });

  it('saves only the changed role with explicit grants', () => {
    create();
    choose('cell-r-care-tasks.complete', 'full');
    expect(button('perm-save').disabled).toBeFalse();

    button('perm-save').click();
    fixture.detectChanges();

    expect(familySpy.updateRolePermissions).toHaveBeenCalledTimes(1);
    expect(familySpy.updateRolePermissions).toHaveBeenCalledWith(
      'r-care',
      { permissions: { 'tasks.view': 'view', 'tasks.complete': 'full' } },
      1
    );
  });

  it('resets role cells to their defaults and clears unsaved changes', () => {
    create();
    choose('cell-r-care-tasks.view', 'full');
    expect(button('perm-save').disabled).toBeFalse();

    button('perm-reset').click();
    fixture.detectChanges();

    const cell = fixture.nativeElement.querySelector('[data-testid="cell-r-care-tasks.view"]') as HTMLSelectElement;
    expect(cell.value).toBe('view');
    expect(button('perm-save').disabled).toBeTrue();
  });

  it('saves a member override and leaves inherited codes out', () => {
    create();
    button('target-m2').click();
    fixture.detectChanges();
    expect(familySpy.getMemberPermissions).toHaveBeenCalledWith('m2');

    choose('override-m2-tasks.view', 'denied');
    button('perm-save').click();
    fixture.detectChanges();

    expect(familySpy.updateMemberPermissions).toHaveBeenCalledWith('m2', { permissions: { 'tasks.view': 'denied' } }, 1);
    expect(familySpy.updateRolePermissions).not.toHaveBeenCalled();
  });

  it('sends the version returned by the last save on the next save', () => {
    create();
    choose('cell-r-care-tasks.complete', 'full');
    button('perm-save').click();
    fixture.detectChanges();

    choose('cell-r-care-tasks.complete', 'allowed');
    button('perm-save').click();
    fixture.detectChanges();

    expect(familySpy.updateRolePermissions).toHaveBeenCalledTimes(2);
    expect(familySpy.updateRolePermissions.calls.mostRecent().args[2]).toBe(2);
  });

  it('offers a reload when a save is rejected with a version conflict', () => {
    familySpy.updateRolePermissions.and.returnValue(throwError(() => ({ code: 'version_conflict', status: 412 })));
    create();
    choose('cell-r-care-tasks.complete', 'full');
    button('perm-save').click();
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('[role="alert"]')).toBeTruthy();
    expect(button('perm-reload')).toBeTruthy();
  });

  it('reloads the server state after a conflict and drops unsaved edits', () => {
    familySpy.updateRolePermissions.and.returnValue(throwError(() => ({ code: 'version_conflict', status: 412 })));
    create();
    choose('cell-r-care-tasks.complete', 'full');
    button('perm-save').click();
    fixture.detectChanges();

    button('perm-reload').click();
    fixture.detectChanges();

    expect(familySpy.listRoles).toHaveBeenCalledTimes(2);
    expect(button('perm-reload')).toBeNull();
    expect(button('perm-save').disabled).toBeTrue();
  });

  it('shows a load error and retry reloads the catalogue when it fails to load', () => {
    familySpy.getCatalogue.and.returnValue(throwError(() => ({ code: 'forbidden', status: 403 })));
    create();
    expect(fixture.nativeElement.querySelector('[data-testid="perm-loading"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('[data-testid="perm-table"]')).toBeNull();
    const retry = fixture.nativeElement.querySelector('[data-testid="load-error-retry"]') as HTMLButtonElement;
    expect(retry).toBeTruthy();

    familySpy.getCatalogue.and.returnValue(of({ permissions: catalogue }));
    retry.click();
    fixture.detectChanges();

    expect(familySpy.getCatalogue).toHaveBeenCalledTimes(2);
    expect(fixture.nativeElement.querySelector('[data-testid="load-error-retry"]')).toBeNull();
  });

  describe('custom roles', () => {
    function customRoles(count: number): RoleView[] {
      return Array.from({ length: count }, (_, i) => ({
        id: `r-custom-${i}`,
        name: `Custom ${i}`,
        description: '',
        is_fixed: false,
        is_built_in: false,
        role_type: 'child',
        permissions: {},
        defaults: {},
        version: 3,
      }));
    }

    it('offers delete only for custom roles', () => {
      familySpy.listRoles.and.returnValue(of({ roles: [...roles, ...customRoles(1)] }));
      create();
      expect(button('delete-role-r-custom-0')).toBeTruthy();
      expect(button('delete-role-r-child')).toBeNull();
      expect(button('delete-role-r-parent')).toBeNull();
    });

    it('deletes a custom role after confirmation and reloads the roles', () => {
      familySpy.listRoles.and.returnValue(of({ roles: [...roles, ...customRoles(1)] }));
      create();
      button('delete-role-r-custom-0').click();
      fixture.detectChanges();
      button('delete-role-confirm').click();
      fixture.detectChanges();

      expect(familySpy.deleteRole).toHaveBeenCalledWith('r-custom-0', 3);
      expect(familySpy.listRoles).toHaveBeenCalledTimes(2);
    });

    it('explains why a role in use cannot be deleted', () => {
      familySpy.listRoles.and.returnValue(of({ roles: [...roles, ...customRoles(1)] }));
      familySpy.deleteRole.and.returnValue(throwError(() => ({ code: 'role_in_use', status: 409 })));
      create();
      fixture.componentInstance.askDeleteRole(customRoles(1)[0]);
      fixture.componentInstance.confirmDeleteRole();

      expect(fixture.componentInstance.roleDeleteError()).toBe('family.permissions.delete_role.error.in_use');
    });

    it('disables Add role at the custom role limit', () => {
      familySpy.listRoles.and.returnValue(of({ roles: [...roles, ...customRoles(2)] }));
      create();
      expect(button('perm-new-role').disabled).toBeTrue();
      expect(fixture.nativeElement.querySelector('[data-testid="perm-role-limit"]')).toBeTruthy();
    });

    it('maps the role limit error when a role is created', () => {
      familySpy.createRole.and.returnValue(throwError(() => ({ code: 'role_limit', status: 409 })));
      create();
      fixture.componentInstance.openNewRole();
      fixture.componentInstance.newRoleForm.setValue({ name: 'Helper', description: '', role_type: 'child' });
      fixture.componentInstance.submitNewRole();

      expect(fixture.componentInstance.roleError()).toBe('family.permissions.new_role.error.limit');
    });
  });
});
