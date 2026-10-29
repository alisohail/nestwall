import { Component, DestroyRef, ElementRef, Injector, OnInit, afterNextRender, computed, inject, signal, viewChild, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { ApiError } from '../../core/api/api-client.service';
import { AuthService } from '../../core/auth/auth.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { IconComponent } from '../../shared/ui/icon/icon.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { LoadErrorComponent } from '../../shared/ui/load-error/load-error.component';
import { COLOUR_PALETTE, ColourPickerComponent } from '../../shared/ui/colour-picker/colour-picker.component';
import { SessionsComponent } from '../sessions/sessions.component';
import { PermissionsEditorComponent } from './permissions-editor/permissions-editor.component';
import { UnsavedChangesAware } from '../../core/unsaved/unsaved-changes';
import { CreateMemberRequest, FamilyService, Gender, MemberDetail, RoleView } from './family.service';

type FamilyTab = 'members' | 'roles' | 'sessions';

const FAMILY_TABS: readonly FamilyTab[] = ['members', 'roles', 'sessions'];

/** Email length limit, matching the server. */
const EMAIL_MAX = 254;

@Component({
    selector: 'app-family',
    imports: [ReactiveFormsModule, RouterLink, TranslocoModule, PermissionsEditorComponent, SessionsComponent, AvatarComponent, IconComponent, LoadingComponent, LoadErrorComponent, ColourPickerComponent],
    templateUrl: './family.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './family.component.scss'
})
export class FamilyComponent implements OnInit, UnsavedChangesAware {
  private fb = inject(FormBuilder);
  private family = inject(FamilyService);
  private auth = inject(AuthService);
  private router = inject(Router);
  private route = inject(ActivatedRoute);
  private destroyRef = inject(DestroyRef);
  private injector = inject(Injector);

  familyName = computed(() => this.auth.currentUser()?.family?.family_name ?? '');
  /** Only the parent holds family.manage, the same permission the server checks for a rename. */
  canManageFamily = computed(() => this.auth.can('family.manage'));
  familyNameEditing = signal(false);
  familyNameSaving = signal(false);
  familyNameError = signal<string | null>(null);
  familyNameInput = viewChild<ElementRef<HTMLInputElement>>('familyNameInput');
  permissionsEditor = viewChild(PermissionsEditorComponent);
  familyNameForm = this.fb.nonNullable.group({
    family_name: ['', [Validators.required, Validators.pattern(/\S/), Validators.maxLength(80)]],
  });

  members = signal<MemberDetail[]>([]);
  childMembers = computed(() => this.members().filter((m) => m.is_child && m.status === 'active'));
  activeCount = computed(() => this.members().filter((m) => m.status === 'active').length);
  allRoles = signal<RoleView[]>([]);
  tab = signal<FamilyTab>('members');
  readonly tabs: FamilyTab[] = [...FAMILY_TABS];
  loading = signal(true);
  loadError = signal<ApiError | null>(null);

  canManageMembers = computed(() => this.auth.can('members.manage'));

  showAddMember = signal(false);
  addMemberSaving = signal(false);
  addMemberError = signal<string | null>(null);

  deleteMember = signal<MemberDetail | null>(null);
  deleting = signal(false);
  deleteError = signal<string | null>(null);

  addMemberForm = this.fb.nonNullable.group({
    role_id: ['', Validators.required],
    display_name: ['', [Validators.required, Validators.maxLength(80)]],
    user_id: ['', [Validators.required, Validators.pattern(/^[a-zA-Z0-9._-]{3,32}$/)]],
    password: ['', [Validators.required, Validators.minLength(8), Validators.maxLength(256)]],
    colour: [COLOUR_PALETTE[0], [Validators.required, Validators.pattern(/^#[0-9A-Fa-f]{6}$/)]],
    pin: ['', [Validators.pattern(/^[0-9]{4,8}$/)]],
    email: ['', [Validators.email, Validators.maxLength(EMAIL_MAX)]],
    date_of_birth: ['', Validators.pattern(/^\d{4}-\d{2}-\d{2}$/)],
    gender: [''],
  });

  ngOnInit(): void {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe();
    const requested = this.route.snapshot.queryParamMap.get('tab');
    if (requested && (FAMILY_TABS as readonly string[]).includes(requested)) this.tab.set(requested as FamilyTab);
    this.addMemberForm.controls.role_id.valueChanges.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => this.applyEmailRule());
    this.reload();
  }

  reload(): void {
    this.loading.set(true);
    this.loadError.set(null);
    this.family
      .listMembers(true)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.members.set(res.members);
          this.loading.set(false);
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loading.set(false);
        },
      });
    this.family
      .listRoles()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.allRoles.set(res.roles),
        error: () => undefined,
      });
  }

  startFamilyNameEdit(): void {
    if (!this.canManageFamily()) return;
    this.familyNameForm.reset({ family_name: this.familyName() });
    this.familyNameError.set(null);
    this.familyNameEditing.set(true);
    afterNextRender(() => this.familyNameInput()?.nativeElement.focus(), { injector: this.injector });
  }

  /** Permissions edits on the Roles tab are the only unsaved values on this page. */
  hasUnsavedChanges(): boolean {
    return this.permissionsEditor()?.hasUnsavedChanges() ?? false;
  }

  saveChanges(): Promise<boolean> {
    return this.permissionsEditor()?.saveChanges() ?? Promise.resolve(false);
  }

  cancelFamilyNameEdit(): void {
    this.familyNameEditing.set(false);
    this.familyNameSaving.set(false);
    this.familyNameError.set(null);
  }

  saveFamilyName(): void {
    if (this.familyNameSaving()) return;
    if (this.familyNameForm.invalid) {
      this.familyNameForm.markAllAsTouched();
      return;
    }
    const name = this.familyNameForm.controls.family_name.value.trim();
    if (name === this.familyName()) {
      this.cancelFamilyNameEdit();
      return;
    }
    this.familyNameSaving.set(true);
    this.familyNameError.set(null);
    this.family
      .updateFamilyName(name)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.auth.setFamilyName(res.family_name);
          this.cancelFamilyNameEdit();
        },
        error: () => {
          this.familyNameSaving.set(false);
          this.familyNameError.set('family.name_edit.error');
        },
      });
  }

  isSelf(member: MemberDetail): boolean {
    return member.user_id === this.auth.currentUser()?.user.user_id;
  }

  /** Parents hold the fixed Parent role. Their role cannot change and they must keep an email. */
  isFixedRole(roleId: string): boolean {
    return this.allRoles().find((r) => r.id === roleId)?.is_fixed ?? false;
  }

  /** Keeps the add-member email rule in step with the chosen role: required for a parent, optional otherwise. */
  private applyEmailRule(): void {
    const email = this.addMemberForm.controls.email;
    email.setValidators(this.isFixedRole(this.addMemberForm.controls.role_id.value) ? [Validators.required, Validators.email, Validators.maxLength(EMAIL_MAX)] : [Validators.email, Validators.maxLength(EMAIL_MAX)]);
    email.updateValueAndValidity({ emitEvent: false });
  }

  /** The signed-in parent can remove anyone else, including another parent, but never themselves. */
  canDelete(member: MemberDetail): boolean {
    return !this.isSelf(member) && this.auth.can('members.manage');
  }

  openAddMember(): void {
    this.addMemberForm.reset({ role_id: '', display_name: '', user_id: '', password: '', colour: COLOUR_PALETTE[0], pin: '', email: '', date_of_birth: '', gender: '' });
    this.addMemberError.set(null);
    this.showAddMember.set(true);
  }

  closeAddMember(): void {
    this.showAddMember.set(false);
  }

  /** The PIN field only applies to the Child role. */
  isChildRole(roleId: string): boolean {
    return this.allRoles().find((r) => r.id === roleId)?.name === 'Child';
  }

  submitAddMember(): void {
    this.addMemberForm.markAllAsTouched();
    if (this.addMemberForm.invalid) return;
    this.addMemberSaving.set(true);
    this.addMemberError.set(null);
    const v = this.addMemberForm.getRawValue();
    const req: CreateMemberRequest = {
      role_id: v.role_id,
      user_id: v.user_id,
      password: v.password,
      display_name: v.display_name,
      colour: v.colour,
    };
    if (this.isChildRole(v.role_id) && v.pin) req.pin = v.pin;
    if (v.email.trim()) req.email = v.email.trim();
    if (v.date_of_birth) req.date_of_birth = v.date_of_birth;
    if (v.gender) req.gender = v.gender as Gender;
    this.family
      .createMember(req)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.addMemberSaving.set(false);
          this.showAddMember.set(false);
          this.reload();
        },
        error: (err: ApiError) => {
          this.addMemberSaving.set(false);
          this.addMemberError.set(this.addMemberErrorKey(err));
        },
      });
  }

  /** Keeps the tab in the URL so the edit page can return to it. */
  setTab(tab: FamilyTab): void {
    this.tab.set(tab);
    this.router.navigate([], { relativeTo: this.route, queryParams: { tab }, replaceUrl: true });
  }

  startDelete(member: MemberDetail): void {
    this.deleteMember.set(member);
    this.deleteError.set(null);
  }

  cancelDelete(): void {
    this.deleteMember.set(null);
    this.deleteError.set(null);
  }

  /** Hard-deletes the member and their history, then drops the card without a reload. */
  confirmDelete(): void {
    const member = this.deleteMember();
    if (!member || this.deleting()) return;
    this.deleting.set(true);
    this.deleteError.set(null);
    this.family
      .removeMember(member.id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.deleting.set(false);
          this.deleteMember.set(null);
          this.members.update((list) => list.filter((m) => m.id !== member.id));
        },
        error: (err: ApiError) => {
          this.deleting.set(false);
          this.deleteError.set(this.deleteErrorKey(err));
        },
      });
  }

  private deleteErrorKey(err: ApiError): string {
    if (err.code === 'cannot_remove_self') return 'family.delete.error.self';
    if (err.code === 'conflict') return 'family.delete.error.last_parent';
    return this.errorKey(err);
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'family.error.network';
    if (err.status === 403) return 'family.error.forbidden';
    return 'family.error.generic';
  }

  private addMemberErrorKey(err: ApiError): string {
    if (err.status === 0) return 'family.error.network';
    if (err.status === 409) return err.code === 'email_taken' ? 'family.add_member.error.email_taken' : 'family.add_member.error.user_id_taken';
    if (err.status === 403) return 'family.error.forbidden';
    if (err.status === 400) return 'family.add_member.error.validation';
    return 'family.error.generic';
  }
}
