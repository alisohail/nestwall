import { Component, DestroyRef, HostListener, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { Observable, catchError, finalize, forkJoin, map, of } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { CalendarImport, CalendarSource, ImportAudience, ImportUpdate, REFRESH_CHOICES, RefreshMinutes } from '../../calendar/calendar.models';
import { CalendarService } from '../../calendar/calendar.service';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { settingsErrorKey } from '../settings.format';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { COLOUR_PALETTE, ColourPickerComponent } from '../../../shared/ui/colour-picker/colour-picker.component';

/** Where a new import comes from: an uploaded .ics file or a calendar link. */
type ImportSource = 'file' | 'link';

/** The kind of calendar the Add dialog creates: an empty manual calendar, or an imported one. */
type NewKind = 'manual' | 'import';

/** Same default as the server for a new linked calendar. */
const DEFAULT_REFRESH: RefreshMinutes = 30;

/** Reasons the server stores when a link fetch fails. Any other code is shown as unavailable. */
const SYNC_ERROR_CODES = ['invalid_url', 'link_not_allowed', 'too_large', 'invalid_ics', 'too_many_events', 'unavailable'];

/** All calendars in one list: manual calendars and imported ICS calendars. Add, edit name and colour, and delete. */
@Component({
  selector: 'app-calendars-settings',
  standalone: true,
  imports: [FormsModule, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent, ColourPickerComponent],
  templateUrl: './calendars-settings.component.html',
  styleUrl: './calendars-settings.component.scss',
})
export class CalendarsSettingsComponent {
  private calendar = inject(CalendarService);
  private family = inject(FamilyService);
  private auth = inject(AuthService);
  private transloco = inject(TranslocoService);
  private destroyRef = inject(DestroyRef);

  /** Grants load asynchronously, so the page waits for them before it decides what to show. */
  permsReady = signal(false);
  canManage = signal(false);

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);
  sources = signal<CalendarSource[]>([]);
  imports = signal<CalendarImport[]>([]);
  /** Manual calendars can only be deleted while another one remains. */
  canDeleteSource = computed(() => this.sources().length > 1);
  members = signal<MemberDetail[]>([]);
  memberNames = computed(() => new Map(this.members().map((m) => [m.id, m.display_name])));

  notice = signal<string | null>(null);
  actionError = signal<string | null>(null);
  confirmingId = signal<string | null>(null);
  deletingId = signal<string | null>(null);
  /** The import whose Sync now, Retry or Resume request is in flight. */
  busyId = signal<string | null>(null);

  // Add and edit dialog
  modalOpen = signal(false);
  saving = signal(false);
  modalError = signal<string | null>(null);
  name = signal('');
  colour = signal(COLOUR_PALETTE[0]);
  /** Add: which kind to create. Edit: manual when the dialog edits a manual calendar. */
  kind = signal<NewKind>('manual');
  isManual = computed(() => this.kind() === 'manual');
  audience = signal<ImportAudience>('everyone');
  showOnWall = signal(true);
  memberIds = signal<string[]>([]);
  source = signal<ImportSource>('file');
  file = signal<File | null>(null);
  url = signal('');
  refreshChoices = REFRESH_CHOICES;
  refreshMinutes = signal<RefreshMinutes>(DEFAULT_REFRESH);
  /** Off means the link is paused and is not fetched. */
  syncOn = signal(true);
  /** The import the dialog edits. Null when the dialog adds a new one. */
  editing = signal<CalendarImport | null>(null);
  editingId = computed(() => this.editing()?.id ?? null);
  /** The manual calendar the dialog edits. Null when it is not editing one. */
  editingSourceId = signal<string | null>(null);
  isEditing = computed(() => this.editing() !== null || this.editingSourceId() !== null);
  editingLink = computed(() => this.editing()?.kind === 'link');
  /** Refresh applies to linked calendars only: the source when adding, the import's kind when editing. */
  showsRefresh = computed(() => !this.isManual() && (this.isEditing() ? this.editingLink() : this.source() === 'link'));

  constructor() {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.canManage.set(this.auth.can('calendar.manage'));
        this.permsReady.set(true);
        if (this.canManage()) this.load();
      });
  }

  load(): void {
    if (!this.canManage()) return;
    this.loadState.set('loading');
    this.loadError.set(null);
    forkJoin({ sources: this.calendar.listSources(), imports: this.calendar.listImports(), members: this.loadMembers() })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ sources, imports, members }) => {
          this.sources.set(sources.items);
          this.imports.set(imports.items);
          this.members.set(members);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  /** Member names are only used for labels and the people picker, so a failure here must not block the page. */
  private loadMembers(): Observable<MemberDetail[]> {
    if (!this.auth.can('members.view')) return of([]);
    return this.family.listMembers().pipe(
      map((res) => res.members),
      catchError(() => of([] as MemberDetail[])),
    );
  }

  private reloadImports(): void {
    this.calendar
      .listImports()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.imports.set(res.items),
        error: (err: ApiError) => this.actionError.set(settingsErrorKey(err)),
      });
  }

  private reloadSources(): void {
    this.calendar
      .listSources()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.sources.set(res.items),
        error: (err: ApiError) => this.actionError.set(settingsErrorKey(err)),
      });
  }

  // List

  /** Audience label for a row, for example "Shared with Ava, Mia". */
  audienceLabel(item: CalendarImport): string {
    if (item.audience === 'everyone') return this.transloco.translate('settings.calendars.audience_everyone');
    if (item.audience === 'parents') return this.transloco.translate('settings.calendars.audience_parents');
    const people = item.member_ids
      .map((id) => this.memberNames().get(id))
      .filter((n): n is string => !!n)
      .join(', ');
    return people
      ? this.transloco.translate('settings.calendars.audience_specific', { people })
      : this.transloco.translate('settings.calendars.option_specific');
  }

  /** The sync time for a row, for example "Synced 12 min ago". Null before the first sync. */
  syncedLabel(item: CalendarImport): string | null {
    if (!item.last_synced_at) return null;
    const minutes = Math.max(0, Math.floor((Date.now() - Date.parse(item.last_synced_at)) / 60000));
    if (minutes < 1) return this.transloco.translate('settings.calendars.synced_now');
    if (minutes < 60) return this.transloco.translate('settings.calendars.synced_minutes', { count: minutes });
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return this.transloco.translate('settings.calendars.synced_hours', { count: hours });
    return this.transloco.translate('settings.calendars.synced_days', { count: Math.floor(hours / 24) });
  }

  /** Why the last fetch failed, in words. */
  syncErrorReason(code: string): string {
    const known = SYNC_ERROR_CODES.includes(code) ? code : 'unavailable';
    return this.transloco.translate(`settings.calendars.sync_error.${known}`);
  }

  /** Fetches a linked calendar now. The server allows one fetch a minute per calendar, so a repeat returns sync_too_soon. */
  syncNow(item: CalendarImport): void {
    if (this.busyId()) return;
    this.busyId.set(item.id);
    this.actionError.set(null);
    this.notice.set(null);
    this.calendar
      .syncImport(item.id)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.busyId.set(null)),
      )
      .subscribe({
        next: (updated) => {
          this.imports.update((list) => list.map((i) => (i.id === updated.id ? updated : i)));
          if (!updated.last_sync_error) this.notice.set(this.transloco.translate('settings.calendars.sync_done'));
        },
        error: (err: ApiError) =>
          this.actionError.set(err.code === 'sync_too_soon' ? 'settings.calendars.error.sync_too_soon' : settingsErrorKey(err)),
      });
  }

  /** Turns a paused link back on. Its next fetch follows the usual schedule. */
  resume(item: CalendarImport): void {
    if (this.busyId()) return;
    this.busyId.set(item.id);
    this.actionError.set(null);
    this.notice.set(null);
    this.calendar
      .updateImport(item.id, { name: item.name, colour: item.colour, paused: false })
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.busyId.set(null)),
      )
      .subscribe({
        next: () => {
          this.notice.set(this.transloco.translate('settings.calendars.resumed'));
          this.reloadImports();
        },
        error: (err: ApiError) => this.actionError.set(settingsErrorKey(err)),
      });
  }

  askDelete(item: { id: string }): void {
    this.actionError.set(null);
    this.notice.set(null);
    this.confirmingId.set(item.id);
  }

  cancelDelete(): void {
    this.confirmingId.set(null);
  }

  remove(item: CalendarImport): void {
    if (this.deletingId()) return;
    this.deletingId.set(item.id);
    this.actionError.set(null);
    this.calendar
      .deleteImport(item.id)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.deletingId.set(null)),
      )
      .subscribe({
        next: () => {
          this.confirmingId.set(null);
          this.imports.update((list) => list.filter((i) => i.id !== item.id));
          this.notice.set(this.transloco.translate('settings.calendars.deleted'));
        },
        error: (err: ApiError) => this.actionError.set(settingsErrorKey(err)),
      });
  }

  /** Deletes a manual calendar and every event on it. The server refuses the last remaining calendar. */
  removeSource(item: CalendarSource): void {
    if (this.deletingId() || !this.canDeleteSource()) return;
    this.deletingId.set(item.id);
    this.actionError.set(null);
    this.calendar
      .deleteSource(item.id)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.deletingId.set(null)),
      )
      .subscribe({
        next: () => {
          this.confirmingId.set(null);
          this.sources.update((list) => list.filter((s) => s.id !== item.id));
          this.notice.set(this.transloco.translate('settings.calendars.deleted'));
        },
        error: (err: ApiError) =>
          this.actionError.set(err.code === 'last_calendar' ? 'settings.calendars.error.last_calendar' : settingsErrorKey(err)),
      });
  }

  // Modal

  openNew(): void {
    this.editing.set(null);
    this.editingSourceId.set(null);
    this.kind.set('import');
    this.name.set('');
    this.colour.set(COLOUR_PALETTE[0]);
    this.audience.set('everyone');
    this.showOnWall.set(true);
    this.memberIds.set([]);
    this.source.set('file');
    this.file.set(null);
    this.url.set('');
    this.refreshMinutes.set(DEFAULT_REFRESH);
    this.syncOn.set(true);
    this.modalError.set(null);
    this.notice.set(null);
    this.modalOpen.set(true);
  }

  /** Switches the Add dialog between a manual calendar and an import. */
  setKind(value: NewKind): void {
    this.kind.set(value);
  }

  openEditSource(item: CalendarSource): void {
    this.editing.set(null);
    this.editingSourceId.set(item.id);
    this.kind.set('manual');
    this.name.set(item.name);
    this.colour.set(item.colour);
    this.modalError.set(null);
    this.notice.set(null);
    this.modalOpen.set(true);
  }

  openEdit(item: CalendarImport): void {
    this.editing.set(item);
    this.editingSourceId.set(null);
    this.kind.set('import');
    this.name.set(item.name);
    this.colour.set(item.colour);
    this.audience.set(item.audience);
    this.showOnWall.set(item.show_on_wall);
    this.memberIds.set([...item.member_ids]);
    this.refreshMinutes.set(item.refresh_minutes);
    this.syncOn.set(!item.paused);
    this.modalError.set(null);
    this.notice.set(null);
    this.modalOpen.set(true);
  }

  closeModal(): void {
    if (this.saving()) return;
    this.modalOpen.set(false);
  }

  closeOnBackdrop(e: Event): void {
    if (e.target === e.currentTarget) this.closeModal();
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.closeModal();
    this.cancelDelete();
  }

  onFileChosen(event: Event): void {
    const input = event.target as HTMLInputElement;
    this.file.set(input.files?.[0] ?? null);
  }

  setSource(value: ImportSource): void {
    this.source.set(value);
  }

  setAudience(value: ImportAudience): void {
    this.audience.set(value);
  }

  toggleMember(id: string): void {
    this.memberIds.update((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]));
  }

  isMemberPicked(id: string): boolean {
    return this.memberIds().includes(id);
  }

  /** The first problem with the dialog values, as a translation key. Null when valid. */
  validationKey(): string | null {
    const name = this.name().trim();
    if (name === '') return 'settings.error.required';
    if (name.length > 80) return 'settings.error.too_long';
    if (this.isManual()) return null;
    if (!this.isEditing()) {
      if (this.source() === 'link') {
        const url = this.url().trim();
        if (url === '') return 'settings.calendars.error.url_required';
        if (!/^https?:\/\//i.test(url)) return 'settings.calendars.error.invalid_url';
      } else if (!this.file()) {
        return 'settings.calendars.error.file_required';
      }
    }
    if (this.audience() === 'specific' && this.memberIds().length === 0) return 'settings.calendars.error.people_required';
    return null;
  }

  /** Server codes from creating an import, mapped to dialog messages. Anything else falls back to the generic mapping. */
  private importErrorKey(err: ApiError): string {
    switch (err.code) {
      case 'invalid_url':
        return 'settings.calendars.error.invalid_url';
      case 'link_not_allowed':
        return 'settings.calendars.error.link_not_allowed';
      case 'calendar_unavailable':
        return 'settings.calendars.error.link_unavailable';
      case 'calendar_too_large':
      case 'too_many_events':
        return 'settings.calendars.error.too_large';
      case 'invalid_ics':
        return 'settings.calendars.error.invalid_ics';
    }
    return settingsErrorKey(err);
  }

  /** The settings an edit sends. Linked calendars also send their refresh and sync state. */
  private updateBody(): ImportUpdate {
    const body: ImportUpdate = {
      name: this.name().trim(),
      colour: this.colour(),
      audience: this.audience(),
      show_on_wall: this.showOnWall(),
    };
    if (this.audience() === 'specific') body.member_ids = this.memberIds();
    if (this.editingLink()) {
      body.refresh_minutes = this.refreshMinutes();
      body.paused = !this.syncOn();
    }
    return body;
  }

  /** Saves the settings of an existing import. Its events read the new values, and an audience change updates them. */
  private saveEdit(id: string): void {
    this.saving.set(true);
    this.calendar
      .updateImport(id, this.updateBody())
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.saving.set(false)),
      )
      .subscribe({
        next: () => {
          this.modalOpen.set(false);
          this.notice.set(this.transloco.translate('settings.calendars.updated'));
          this.reloadImports();
        },
        error: (err: ApiError) => this.modalError.set(settingsErrorKey(err)),
      });
  }

  /** Saves a manual calendar's name and colour, either edited or new. */
  private saveSource(id: string | null): void {
    const body = { name: this.name().trim(), colour: this.colour() };
    const request = id ? this.calendar.updateSource(id, body) : this.calendar.createSource(body);
    this.saving.set(true);
    request
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.saving.set(false)),
      )
      .subscribe({
        next: () => {
          this.modalOpen.set(false);
          this.notice.set(this.transloco.translate(id ? 'settings.calendars.updated' : 'settings.calendars.manual_added'));
          this.reloadSources();
        },
        error: (err: ApiError) => this.modalError.set(settingsErrorKey(err)),
      });
  }

  save(): void {
    if (this.saving()) return;
    this.modalError.set(null);
    const problem = this.validationKey();
    if (problem) {
      this.modalError.set(problem);
      return;
    }
    const editSourceId = this.editingSourceId();
    if (editSourceId) {
      this.saveSource(editSourceId);
      return;
    }
    const editId = this.editingId();
    if (editId) {
      this.saveEdit(editId);
      return;
    }
    if (this.isManual()) {
      this.saveSource(null);
      return;
    }
    const linked = this.source() === 'link';
    const file = this.file();
    if (!linked && !file) return;
    const form = new FormData();
    if (linked) {
      form.append('url', this.url().trim());
      form.append('refresh_minutes', String(this.refreshMinutes()));
    } else if (file) {
      form.append('file', file);
    }
    form.append('name', this.name().trim());
    form.append('colour', this.colour());
    form.append('audience', this.audience());
    form.append('show_on_wall', this.showOnWall() ? 'true' : 'false');
    if (this.audience() === 'specific') {
      for (const id of this.memberIds()) form.append('member_ids', id);
    }
    this.saving.set(true);
    this.calendar
      .createImport(form)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.saving.set(false)),
      )
      .subscribe({
        next: (created) => {
          this.modalOpen.set(false);
          this.notice.set(
            created.skipped_count > 0
              ? this.transloco.translate('settings.calendars.imported_skipped', {
                  count: created.event_count,
                  skipped: created.skipped_count,
                })
              : this.transloco.translate('settings.calendars.imported', { count: created.event_count }),
          );
          this.reloadImports();
        },
        error: (err: ApiError) => this.modalError.set(this.importErrorKey(err)),
      });
  }
}
