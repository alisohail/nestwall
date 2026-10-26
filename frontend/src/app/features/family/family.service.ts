import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';

export type Gender = 'female' | 'male' | 'prefer_not_to_say';

export interface MemberDetail {
  id: string;
  user_id: string;
  display_name: string;
  role_id: string;
  role_name: string;
  status: string;
  colour: string;
  is_child: boolean;
  avatar_id: string | null;
  email: string | null;
  /** YYYY-MM-DD, or null when not set. */
  date_of_birth: string | null;
  gender: Gender | null;
}

/** Only the fields present are changed. An empty string clears email, date_of_birth or gender. */
export interface UpdateMemberRequest {
  display_name?: string;
  colour?: string;
  role_id?: string;
  status?: string;
  user_id?: string;
  pin?: string;
  email?: string;
  date_of_birth?: string;
  gender?: string;
  /** Admin-set password for the member. Does not force a change at next sign-in. */
  password?: string;
}

export type AccessLevel = 'denied' | 'allowed' | 'view' | 'own' | 'request' | 'full';

/** Grants keyed by permission code. A missing code means no grant on roles and no override on members. */
export type Grants = Record<string, AccessLevel>;

export interface RoleView {
  id: string;
  name: string;
  description: string;
  /** 'child', 'co_parent' or 'parent' (the fixed Parent role). */
  role_type: RoleType;
  is_fixed: boolean;
  /** Seeded roles (Child, Parent) cannot be deleted. Custom roles are the rest. */
  is_built_in: boolean;
  permissions: Grants;
  defaults: Grants;
  /** Optimistic concurrency version, sent back as If-Match on writes. */
  version: number;
}

export type RoleType = 'child' | 'co_parent' | 'parent';

/** Which role types may hold a code: any, co_parent (co-parent and Parent roles) or parent (Parent role only). */
export type PermissionAudience = 'any' | 'co_parent' | 'parent';

export interface PermissionEntry {
  code: string;
  description: string;
  audience: PermissionAudience;
  levels: AccessLevel[];
}

export interface CatalogueResponse {
  permissions: PermissionEntry[];
}

export interface MemberPermissionsResponse {
  is_parent: boolean;
  permissions: Grants;
  version: number;
}

export interface SetPermissionsRequest {
  permissions: Grants;
}

/** Returned by permission writes. Version is the new version after the save. */
export interface SaveResponse {
  version: number;
}

export interface CreateRoleRequest {
  name: string;
  description: string;
  /** Defaults to 'child' on the server. */
  role_type?: 'child' | 'co_parent';
}

export interface CreateRoleResponse {
  id: string;
}

export interface CreateMemberRequest {
  role_id: string;
  user_id: string;
  password: string;
  display_name: string;
  colour: string;
  pin?: string;
  email?: string;
  date_of_birth?: string;
  gender?: Gender;
}

export interface UpdateFamilyNameResponse {
  family_name: string;
}

@Injectable({ providedIn: 'root' })
export class FamilyService {
  private api = inject(ApiClient);

  /** Inactive members are included only when includeInactive is true (the Family page). */
  listMembers(includeInactive = false): Observable<{ members: MemberDetail[] }> {
    const query = includeInactive ? '?include_inactive=true' : '';
    return this.api.get<{ members: MemberDetail[] }>(`/api/v1/family/members${query}`);
  }

  listRoles(): Observable<{ roles: RoleView[] }> {
    return this.api.get<{ roles: RoleView[] }>('/api/v1/family/roles');
  }

  createMember(req: CreateMemberRequest): Observable<void> {
    return this.api.post<void>('/api/v1/family/members', req);
  }

  updateFamilyName(familyName: string): Observable<UpdateFamilyNameResponse> {
    return this.api.put<UpdateFamilyNameResponse>('/api/v1/family', { family_name: familyName });
  }

  updateMember(memberId: string, req: UpdateMemberRequest): Observable<void> {
    return this.api.put<void>(`/api/v1/family/members/${memberId}`, req);
  }

  /** Hard-deletes the member and their history. Requires members.manage. */
  removeMember(memberId: string): Observable<void> {
    return this.api.delete<void>(`/api/v1/family/members/${memberId}`);
  }

  getCatalogue(): Observable<CatalogueResponse> {
    return this.api.get<CatalogueResponse>('/api/v1/permissions/catalogue');
  }

  createRole(req: CreateRoleRequest): Observable<CreateRoleResponse> {
    return this.api.post<CreateRoleResponse>('/api/v1/family/roles', req);
  }

  /** version is the role version the delete was based on. A stale version fails with 412. */
  deleteRole(roleId: string, version: number): Observable<void> {
    return this.api.delete<void>(`/api/v1/family/roles/${roleId}`, version);
  }

  /** version is the role version the edit was based on. A stale version fails with 412. */
  updateRolePermissions(roleId: string, req: SetPermissionsRequest, version: number): Observable<SaveResponse> {
    return this.api.put<SaveResponse>(`/api/v1/family/roles/${roleId}/permissions`, req, version);
  }

  getMemberPermissions(memberId: string): Observable<MemberPermissionsResponse> {
    return this.api.get<MemberPermissionsResponse>(`/api/v1/family/members/${memberId}/permissions`);
  }

  /** version is the member overrides version the edit was based on. A stale version fails with 412. */
  updateMemberPermissions(memberId: string, req: SetPermissionsRequest, version: number): Observable<SaveResponse> {
    return this.api.put<SaveResponse>(`/api/v1/family/members/${memberId}/permissions`, req, version);
  }
}
