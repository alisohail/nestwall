import { Component, DestroyRef, HostListener, OnInit, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { forkJoin } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { OwnPasswordComponent } from '../../../shared/own-password/own-password.component';
import { PhotoEditorComponent } from '../../../shared/photo-editor/photo-editor.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { COLOUR_PALETTE, ColourPickerComponent } from '../../../shared/ui/colour-picker/colour-picker.component';
import { FamilyService, Gender, MemberDetail, RoleView, UpdateMemberRequest } from '../family.service';
import { BIRTH_DATE_MIN, EMAIL_MAX, NAME_MAX, birthDateRange, todayIso } from '../../../shared/profile/profile-fields';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';

const USER_ID_PATTERN = /^[a-zA-Z0-9._-]{3,32}$/;

@Component({
    selector: 'app-member-edit',
    imports: [ReactiveFormsModule, RouterLink, TranslocoModule, AvatarComponent, IconComponent, LoadingComponent, LoadErrorComponent, OwnPasswordComponent, PhotoEditorComponent, ColourPickerComponent],
    templateUrl: './member-edit.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './member-edit.component.scss'
})
export class MemberEditComponent implements OnInit, UnsavedChangesAware {
  private fb = inject(FormBuilder);
  private family = inject(FamilyService);
  private auth = inject(AuthService);
  private route = inject(ActivatedRoute);
  private router = inject(Router);
  private destroyRef = inject(DestroyRef);

  readonly today = todayIso();
  readonly birthDateMin = BIRTH_DATE_MIN;
  readonly genders: Gender[] = ['female', 'male', 'prefer_not_to_say'];
  /** Back and save both return here, so the Members tab is reopened. */
  readonly familyMembersTab = { tab: 'members' };

  memberId = '';
  member = signal<MemberDetail | null>(null);
  roles = signal<RoleView[]>([]);
  loading = signal(true);
  loadError = signal<ApiError | null>(null);
  saving = signal(false);
  saveError = signal<string | null>(null);
  photoOpen = signal(false);
  passwordOpen = signal(false);

  canManage = computed(() => this.auth.can('members.manage'));
  /** Roles a non-parent can be moved to. Parent is fixed and cannot be assigned. */
  assignableRoles = computed(() => this.roles().filter((r) => !r.is_fixed));
  isSelf = computed(() => this.member()?.user_id === this.auth.currentUser()?.user.user_id);
  isParent = computed(() => {
    const m = this.member();
    return !!m && (this.roles().find((r) => r.id === m.role_id)?.is_fixed ?? false);
  });

  form = this.fb.nonNullable.group({
    display_name: ['', [Validators.required, Validators.pattern(/\S/), Validators.maxLength(NAME_MAX)]],
    colour: [COLOUR_PALETTE[0]],
    role_id: [''],
    user_id: ['', [Validators.required, Validators.pattern(USER_ID_PATTERN)]],
    active: [true],
    email: ['', [Validators.email, Validators.maxLength(EMAIL_MAX)]],
    date_of_birth: ['', [birthDateRange]],
    gender: [''],
    pin: ['', [Validators.pattern(/^\d{4,8}$/)]],
    password: ['', [Validators.minLength(8), Validators.maxLength(256)]],
  });

  ngOnInit(): void {
    this.memberId = this.route.snapshot.paramMap.get('id') ?? '';
    this.load();
  }

  load(): void {
    this.loading.set(true);
    this.loadError.set(null);
    forkJoin({
      members: this.family.listMembers(true),
      roles: this.family.listRoles(),
      permissions: this.auth.loadPermissions(),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ members, roles }) => {
          this.roles.set(roles.roles);
          const found = members.members.find((m) => m.id === this.memberId) ?? null;
          this.member.set(found);
          if (found) this.fillForm(found);
          this.loading.set(false);
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loading.set(false);
        },
      });
  }

  private fillForm(m: MemberDetail): void {
    this.form.reset({
      display_name: m.display_name,
      colour: m.colour,
      role_id: m.role_id,
      user_id: m.user_id,
      active: m.status === 'active',
      email: m.email ?? '',
      date_of_birth: m.date_of_birth ?? '',
      gender: m.gender ?? '',
      pin: '',
      password: '',
    });
    this.applyEmailRule();
    if (!this.canManage()) this.form.disable({ emitEvent: false });
  }

  /** Parents must keep an email, since it is the only way to reset their password. */
  private applyEmailRule(): void {
    const email = this.form.controls.email;
    email.setValidators(
      this.isParent() ? [Validators.required, Validators.email, Validators.maxLength(EMAIL_MAX)] : [Validators.email, Validators.maxLength(EMAIL_MAX)]
    );
    email.updateValueAndValidity({ emitEvent: false });
  }

  /** Sends only the fields that changed. An empty string clears email, date of birth or gender. */
  buildRequest(): UpdateMemberRequest {
    const m = this.member();
    const req: UpdateMemberRequest = {};
    if (!m) return req;
    const v = this.form.getRawValue();

    const name = v.display_name.trim();
    if (name !== m.display_name) req.display_name = name;
    if (v.colour !== m.colour) req.colour = v.colour;
    if (!this.isParent() && v.role_id !== m.role_id) req.role_id = v.role_id;
    const userId = v.user_id.trim();
    if (userId !== m.user_id) req.user_id = userId;
    const status = v.active ? 'active' : 'inactive';
    if (!this.isSelf() && status !== m.status) req.status = status;
    const email = v.email.trim().toLowerCase();
    if (email !== (m.email ?? '')) req.email = email;
    if (v.date_of_birth !== (m.date_of_birth ?? '')) req.date_of_birth = v.date_of_birth;
    if (v.gender !== (m.gender ?? '')) req.gender = v.gender;
    if (m.is_child && v.pin) req.pin = v.pin;
    if (!this.isSelf() && v.password) req.password = v.password;
    return req;
  }

  hasChanges(): boolean {
    return Object.keys(this.buildRequest()).length > 0;
  }

  hasUnsavedChanges(): boolean {
    return !this.loading() && this.hasChanges();
  }

  @HostListener('document:keydown.escape')
  closeModals(): void {
    this.photoOpen.set(false);
    this.passwordOpen.set(false);
  }

  save(): void {
    void this.saveChanges().then((ok) => {
      if (ok) void this.router.navigate(['/family'], { queryParams: this.familyMembersTab });
    });
  }

  saveChanges(): Promise<boolean> {
    const m = this.member();
    if (!m || this.saving() || !this.canManage()) return Promise.resolve(false);
    this.form.markAllAsTouched();
    this.saveError.set(null);
    if (this.form.invalid || !this.hasChanges()) return Promise.resolve(false);

    const req = this.buildRequest();
    this.saving.set(true);
    return new Promise<boolean>((resolve) => {
      this.family
        .updateMember(m.id, req)
        .pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe({
          next: () => {
            this.saving.set(false);
            this.applySaved(m, req);
            resolve(true);
          },
          error: (err: ApiError) => {
            this.saving.set(false);
            this.saveError.set(this.saveErrorKey(err));
            resolve(false);
          },
        });
    });
  }

  /** Makes the saved values the new baseline, so leaving right after a save does not ask about unsaved changes. */
  private applySaved(m: MemberDetail, req: UpdateMemberRequest): void {
    const saved: MemberDetail = {
      ...m,
      display_name: req.display_name ?? m.display_name,
      colour: req.colour ?? m.colour,
      role_id: req.role_id ?? m.role_id,
      user_id: req.user_id ?? m.user_id,
      status: req.status ?? m.status,
      email: req.email ?? m.email,
      date_of_birth: req.date_of_birth ?? m.date_of_birth,
      gender: req.gender !== undefined ? (req.gender || null) as MemberDetail['gender'] : m.gender,
    };
    this.member.set(saved);
    this.fillForm(saved);
  }

  onPhotoChanged(avatarId: string | null): void {
    this.member.update((m) => (m ? { ...m, avatar_id: avatarId } : m));
  }

  private saveErrorKey(err: ApiError): string {
    if (err.status === 0) return 'family.error.network';
    switch (err.code) {
      case 'user_id_taken':
        return 'family.edit.error.user_id_taken';
      case 'email_taken':
        return 'family.edit.error.email_taken';
      case 'last_parent':
        return 'family.edit.error.last_parent';
      case 'cannot_deactivate_self':
        return 'family.edit.error.cannot_deactivate_self';
      case 'invalid_target':
        return 'family.edit.error.invalid_target';
    }
    if (err.status === 400) return 'family.edit.error.invalid';
    if (err.status === 403) return 'family.error.forbidden';
    return 'family.error.generic';
  }
}
