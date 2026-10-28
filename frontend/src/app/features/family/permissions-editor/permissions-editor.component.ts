import { Component, DestroyRef, OnInit, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable, forkJoin } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { AccessLevel, FamilyService, Grants, MemberDetail, PermissionAudience, PermissionEntry, RoleType, RoleView, SaveResponse } from '../family.service';

/** Target key for editing the role defaults. Any other target is a member id. */
const ROLE_TARGET = 'roles';
/** Override value that means "no override": the member uses the role setting. */
const INHERIT = 'inherit' as const;
const ROLE_PREFIX = 'role:';
const MEMBER_PREFIX = 'member:';
/** Most custom roles a family can have. Seeded roles do not count. */
const MAX_CUSTOM_ROLES = 2;
const DEFAULT_LEVEL: AccessLevel = 'denied';
const GROUP_ORDER = ['tasks', 'rewards', 'calendar', 'family'] as const;
/** Permissions left out of the matrix. The server still enforces them with their stored grants. */
const HIDDEN_CODES = new Set(['profile.avatar.change']);

type GroupKey = (typeof GROUP_ORDER)[number];
type Level = AccessLevel | typeof INHERIT;

interface PermissionGroup {
  key: GroupKey;
  rows: PermissionEntry[];
}

interface SaveJob {
  key: string;
  snapshot: Grants;
  request: Observable<SaveResponse>;
}

function roleKey(id: string): string {
  return ROLE_PREFIX + id;
}

function memberKey(id: string): string {
  return MEMBER_PREFIX + id;
}

function groupOf(code: string): GroupKey {
  const prefix = code.split('.')[0];
  return prefix === 'tasks' || prefix === 'rewards' || prefix === 'calendar' ? prefix : 'family';
}

/** True when a role type may hold a permission with this audience. The Parent role holds everything. */
function audienceAllows(audience: PermissionAudience, roleType: RoleType): boolean {
  if (roleType === 'parent' || audience === 'any') return true;
  return audience === 'co_parent' && roleType === 'co_parent';
}

@Component({
    selector: 'app-permissions-editor',
    imports: [ReactiveFormsModule, TranslocoModule, AvatarComponent, IconComponent, LoadingComponent, LoadErrorComponent],
    templateUrl: './permissions-editor.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './permissions-editor.component.scss'
})
export class PermissionsEditorComponent implements OnInit, UnsavedChangesAware {
  private fb = inject(FormBuilder);
  private family = inject(FamilyService);
  private destroyRef = inject(DestroyRef);

  readonly roleTarget = ROLE_TARGET;

  catalogue = signal<PermissionEntry[]>([]);
  roles = signal<RoleView[]>([]);
  members = signal<MemberDetail[]>([]);
  loading = signal(true);
  loadError = signal<ApiError | null>(null);

  target = signal<string>(ROLE_TARGET);
  /** Unsaved edits per target key (roles:<id> or member:<id>). */
  drafts = signal<Record<string, Grants>>({});
  /** Last known server state per target key. */
  saved = signal<Record<string, Grants>>({});
  /** Version of the server state each target key was loaded or last saved at. Sent as If-Match. */
  versions = signal<Record<string, number>>({});

  saving = signal(false);
  actionError = signal<string | null>(null);
  /** True when the last save was rejected because someone else changed the permissions. */
  conflict = signal(false);
  notice = signal<string | null>(null);

  newRoleOpen = signal(false);
  roleSaving = signal(false);
  roleError = signal<string | null>(null);

  newRoleForm = this.fb.nonNullable.group({
    name: ['', [Validators.required, Validators.pattern(/\S/), Validators.maxLength(40)]],
    description: ['', [Validators.maxLength(200)]],
    role_type: ['child' as 'child' | 'co_parent', [Validators.required]],
  });

  /** Role id awaiting delete confirmation, or null. */
  deleteRoleId = signal<string | null>(null);
  roleDeleting = signal(false);
  roleDeleteError = signal<string | null>(null);

  readonly maxCustomRoles = MAX_CUSTOM_ROLES;
  readonly editableRoles = computed(() => this.roles().filter((r) => !r.is_fixed));
  readonly customRoles = computed(() => this.roles().filter((r) => !r.is_fixed && !r.is_built_in));
  readonly canAddRole = computed(() => this.customRoles().length < MAX_CUSTOM_ROLES);
  readonly roleToDelete = computed(() => this.roles().find((r) => r.id === this.deleteRoleId()) ?? null);
  readonly isRoleMode = computed(() => this.target() === ROLE_TARGET);
  readonly selectedMember = computed(() => this.members().find((m) => m.id === this.target()) ?? null);
  /** Role type of the selected member's role. Member rows are filtered by it. */
  readonly memberRoleType = computed<RoleType>(() => {
    const roleName = this.selectedMember()?.role_name;
    return this.roles().find((r) => r.name === roleName)?.role_type ?? 'child';
  });
  readonly columnCount = computed(() => (this.isRoleMode() ? 2 + this.editableRoles().length : 3));

  readonly groups = computed<PermissionGroup[]>(() => {
    const memberType = this.memberRoleType();
    const byGroup = new Map<GroupKey, PermissionEntry[]>();
    for (const entry of this.catalogue()) {
      if (!this.isRoleMode() && !audienceAllows(entry.audience, memberType)) continue;
      const key = groupOf(entry.code);
      byGroup.set(key, [...(byGroup.get(key) ?? []), entry]);
    }
    return GROUP_ORDER.filter((key) => byGroup.has(key)).map((key) => ({ key, rows: byGroup.get(key) ?? [] }));
  });

  /** Number of cells with changes that are not saved yet, across all targets. */
  readonly unsavedCount = computed(() => {
    const drafts = this.drafts();
    const saved = this.saved();
    let count = 0;
    for (const role of this.editableRoles()) {
      const key = roleKey(role.id);
      count += this.diffCount(drafts[key], saved[key], DEFAULT_LEVEL);
    }
    for (const key of Object.keys(drafts)) {
      if (key.startsWith(MEMBER_PREFIX)) {
        count += this.diffCount(drafts[key], saved[key], INHERIT);
      }
    }
    return count;
  });

  /** Picks the singular or plural copy for the modified-count tally. */
  modCountKey(): string {
    const one = this.modifiedCount() === 1 ? '_one' : '_other';
    return this.isRoleMode()
      ? `family.permissions.changed_from_defaults${one}`
      : `family.permissions.overrides${one}`;
  }

  /** Cells that differ from the role defaults, or overrides for the selected person. */
  readonly modifiedCount = computed(() => {
    const drafts = this.drafts();
    if (!this.isRoleMode()) {
      return Object.keys(drafts[memberKey(this.target())] ?? {}).length;
    }
    return this.editableRoles().reduce(
      (sum, role) => sum + this.diffCount(drafts[roleKey(role.id)], role.defaults, DEFAULT_LEVEL),
      0
    );
  });

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading.set(true);
    this.loadError.set(null);
    forkJoin({
      catalogue: this.family.getCatalogue(),
      roles: this.family.listRoles(),
      members: this.family.listMembers(),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ catalogue, roles, members }) => {
          this.catalogue.set(catalogue.permissions.filter((p) => !HIDDEN_CODES.has(p.code)));
          this.members.set(members.members.filter((m) => m.role_name !== 'Parent' && m.status === 'active'));
          this.applyRoles(roles.roles);
          this.loading.set(false);
          this.selectTarget(this.target());
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loading.set(false);
        },
      });
  }

  /** Adds state for roles not seen before. Existing unsaved edits are kept. */
  private applyRoles(roles: RoleView[]): void {
    this.roles.set(roles);
    const drafts = { ...this.drafts() };
    const saved = { ...this.saved() };
    const versions = { ...this.versions() };
    for (const role of roles) {
      const key = roleKey(role.id);
      if (role.is_fixed || saved[key]) continue;
      saved[key] = { ...role.permissions };
      drafts[key] = { ...role.permissions };
      versions[key] = role.version;
    }
    this.drafts.set(drafts);
    this.saved.set(saved);
    this.versions.set(versions);
  }

  selectTarget(id: string): void {
    this.target.set(id);
    this.actionError.set(null);
    this.notice.set(null);
    if (id === ROLE_TARGET) return;

    const key = memberKey(id);
    if (this.saved()[key]) return;
    this.family
      .getMemberPermissions(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.saved.update((s) => ({ ...s, [key]: { ...res.permissions } }));
          this.drafts.update((d) => ({ ...d, [key]: { ...res.permissions } }));
          this.versions.update((v) => ({ ...v, [key]: res.version }));
        },
        error: (err: ApiError) => this.actionError.set(this.errorKey(err)),
      });
  }

  levelOptions(entry: PermissionEntry): AccessLevel[] {
    return entry.levels;
  }

  /** False when the role type can never hold this permission. The matrix shows a dash instead of a select. */
  roleAllows(role: RoleView, entry: PermissionEntry): boolean {
    return audienceAllows(entry.audience, role.role_type);
  }

  roleLevel(role: RoleView, code: string): AccessLevel {
    return this.drafts()[roleKey(role.id)]?.[code] ?? DEFAULT_LEVEL;
  }

  isRoleModified(role: RoleView, code: string): boolean {
    return this.roleLevel(role, code) !== (role.defaults[code] ?? DEFAULT_LEVEL);
  }

  onRoleSelect(role: RoleView, code: string, event: Event): void {
    const value = (event.target as HTMLSelectElement).value as AccessLevel;
    this.updateDraft(roleKey(role.id), code, value);
  }

  overrideOptions(entry: PermissionEntry): Level[] {
    return [INHERIT, ...this.levelOptions(entry)];
  }

  overrideValue(code: string): Level {
    return this.drafts()[memberKey(this.target())]?.[code] ?? INHERIT;
  }

  hasOverride(code: string): boolean {
    return this.overrideValue(code) !== INHERIT;
  }

  onOverrideSelect(code: string, event: Event): void {
    const value = (event.target as HTMLSelectElement).value as Level;
    this.updateDraft(memberKey(this.target()), code, value);
  }

  roleDefaultLevel(code: string): AccessLevel {
    const roleName = this.selectedMember()?.role_name;
    const role = this.roles().find((r) => r.name === roleName);
    return role?.permissions[code] ?? DEFAULT_LEVEL;
  }

  overrideCount(memberId: string): number {
    return Object.keys(this.drafts()[memberKey(memberId)] ?? {}).length;
  }

  resetToDefaults(): void {
    if (this.isRoleMode()) {
      const drafts = { ...this.drafts() };
      for (const role of this.editableRoles()) {
        drafts[roleKey(role.id)] = { ...role.defaults };
      }
      this.drafts.set(drafts);
    } else {
      this.drafts.update((d) => ({ ...d, [memberKey(this.target())]: {} }));
    }
    this.notice.set(null);
  }

  discardChanges(): void {
    this.drafts.set({ ...this.saved() });
    this.notice.set(null);
    this.actionError.set(null);
  }

  save(): void {
    void this.saveChanges();
  }

  hasUnsavedChanges(): boolean {
    return !this.loading() && !this.loadError() && this.unsavedCount() > 0;
  }

  saveChanges(): Promise<boolean> {
    if (this.saving() || this.unsavedCount() === 0) return Promise.resolve(false);
    const drafts = this.drafts();
    const saved = this.saved();
    const versions = this.versions();
    const jobs: SaveJob[] = [];

    for (const role of this.editableRoles()) {
      const key = roleKey(role.id);
      if (this.diffCount(drafts[key], saved[key], DEFAULT_LEVEL) === 0) continue;
      const snapshot = { ...drafts[key] };
      jobs.push({
        key,
        snapshot,
        request: this.family.updateRolePermissions(
          role.id,
          { permissions: this.explicitGrants(snapshot) },
          versions[key]
        ),
      });
    }

    for (const key of Object.keys(drafts)) {
      if (!key.startsWith(MEMBER_PREFIX) || this.diffCount(drafts[key], saved[key], INHERIT) === 0) continue;
      const snapshot = { ...drafts[key] };
      jobs.push({
        key,
        snapshot,
        request: this.family.updateMemberPermissions(
          key.slice(MEMBER_PREFIX.length),
          { permissions: snapshot },
          versions[key]
        ),
      });
    }

    if (jobs.length === 0) return Promise.resolve(false);
    this.saving.set(true);
    this.actionError.set(null);
    this.conflict.set(false);
    this.notice.set(null);

    return new Promise<boolean>((resolve) => {
      forkJoin(jobs.map((job) => job.request))
        .pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe({
          next: (responses) => {
            const snapshots: Record<string, Grants> = Object.fromEntries(jobs.map((job) => [job.key, job.snapshot]));
            const nextVersions: Record<string, number> = { ...this.versions() };
            jobs.forEach((job, i) => (nextVersions[job.key] = responses[i].version));
            this.versions.set(nextVersions);
            this.saved.update((s) => ({ ...s, ...snapshots }));
            this.roles.update((list) =>
              list.map((r) => {
                const key = roleKey(r.id);
                return key in snapshots
                  ? { ...r, permissions: { ...snapshots[key] }, version: nextVersions[key] }
                  : r;
              })
            );
            this.saving.set(false);
            this.notice.set('family.permissions.saved');
            resolve(true);
          },
          error: (err: ApiError) => {
            this.saving.set(false);
            this.conflict.set(err.status === 412);
            this.actionError.set(this.errorKey(err));
            resolve(false);
          },
        });
    });
  }

  /** Drops unsaved edits and reloads the server state after a version conflict. */
  reloadAfterConflict(): void {
    this.saved.set({});
    this.drafts.set({});
    this.versions.set({});
    this.conflict.set(false);
    this.actionError.set(null);
    this.notice.set(null);
    this.load();
  }

  openNewRole(): void {
    this.newRoleForm.reset({ name: '', description: '', role_type: 'child' });
    this.roleError.set(null);
    this.newRoleOpen.set(true);
  }

  closeNewRole(): void {
    this.newRoleOpen.set(false);
  }

  submitNewRole(): void {
    this.newRoleForm.markAllAsTouched();
    if (this.newRoleForm.invalid || this.roleSaving()) return;
    this.roleSaving.set(true);
    this.roleError.set(null);
    const v = this.newRoleForm.getRawValue();
    this.family
      .createRole({ name: v.name.trim(), description: v.description.trim(), role_type: v.role_type })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.roleSaving.set(false);
          this.newRoleOpen.set(false);
          this.reloadRoles();
        },
        error: (err: ApiError) => {
          this.roleSaving.set(false);
          this.roleError.set(this.roleErrorKey(err));
        },
      });
  }

  private reloadRoles(): void {
    this.family
      .listRoles()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.applyRoles(res.roles),
        error: (err: ApiError) => this.actionError.set(this.errorKey(err)),
      });
  }

  askDeleteRole(role: RoleView): void {
    this.deleteRoleId.set(role.id);
    this.roleDeleteError.set(null);
  }

  cancelDeleteRole(): void {
    this.deleteRoleId.set(null);
    this.roleDeleteError.set(null);
  }

  confirmDeleteRole(): void {
    const role = this.roleToDelete();
    if (!role || this.roleDeleting()) return;
    this.roleDeleting.set(true);
    this.roleDeleteError.set(null);
    this.family
      .deleteRole(role.id, role.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.roleDeleting.set(false);
          this.deleteRoleId.set(null);
          this.forgetRole(role.id);
          this.reloadRoles();
        },
        error: (err: ApiError) => {
          this.roleDeleting.set(false);
          if (err.status === 412) this.reloadRoles();
          this.roleDeleteError.set(this.roleDeleteErrorKey(err));
        },
      });
  }

  /** Removes the state kept for a deleted role so it no longer counts as unsaved. */
  private forgetRole(id: string): void {
    const key = roleKey(id);
    const without = <T>(record: Record<string, T>): Record<string, T> =>
      Object.fromEntries(Object.entries(record).filter(([k]) => k !== key));
    this.drafts.update(without);
    this.saved.update(without);
    this.versions.update(without);
  }

  codeKey(code: string): string {
    return 'family.permissions.codes.' + code.replace(/\./g, '_');
  }

  levelKey(level: string): string {
    return level === INHERIT ? 'family.permissions.inherit' : 'family.permissions.levels.' + level;
  }

  private updateDraft(key: string, code: string, value: Level): void {
    this.drafts.update((d) => {
      const next = { ...(d[key] ?? {}) };
      if (value === INHERIT) {
        delete next[code];
      } else {
        next[code] = value;
      }
      return { ...d, [key]: next };
    });
    this.notice.set(null);
  }

  /** Counts codes whose draft value differs from the reference value. Missing values use base. */
  private diffCount(draft: Grants | undefined, reference: Grants | undefined, base: Level): number {
    let count = 0;
    for (const entry of this.catalogue()) {
      if ((draft?.[entry.code] ?? base) !== (reference?.[entry.code] ?? base)) count++;
    }
    return count;
  }

  /** Roles store only grants above denied. Denied is the default when a code is absent. */
  private explicitGrants(grants: Grants): Grants {
    return Object.fromEntries(Object.entries(grants).filter(([, level]) => level !== DEFAULT_LEVEL));
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'family.permissions.error.network';
    if (err.status === 403) return 'family.permissions.error.forbidden';
    if (err.status === 409) return 'family.permissions.error.parent_locked';
    if (err.status === 412) return 'family.permissions.error.conflict';
    if (err.status === 400) return 'family.permissions.error.validation';
    return 'family.permissions.error.generic';
  }

  private roleErrorKey(err: ApiError): string {
    if (err.code === 'role_limit') return 'family.permissions.new_role.error.limit';
    if (err.status === 409) return 'family.permissions.new_role.error.name_taken';
    return this.errorKey(err);
  }

  private roleDeleteErrorKey(err: ApiError): string {
    if (err.code === 'role_in_use') return 'family.permissions.delete_role.error.in_use';
    if (err.code === 'built_in_role' || err.code === 'parent_locked') return 'family.permissions.delete_role.error.built_in';
    if (err.status === 412) return 'family.permissions.delete_role.error.conflict';
    return this.errorKey(err);
  }
}
