
import { Component, DestroyRef, inject, signal, DOCUMENT } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable, concatMap, finalize, from, map } from 'rxjs';
import { ApiClient } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { CalendarService } from '../../calendar/calendar.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { BackupsCardComponent } from './backups/backups-card.component';

interface ExportFile {
  name: string;
  fetch: () => Observable<Blob>;
}

/** Data and backups: the backups card and the "Export for reading" card. Reset comes in a later step. */
@Component({
    selector: 'app-data-settings',
    imports: [TranslocoModule, BannerComponent, IconComponent, LoadingComponent, BackupsCardComponent],
    templateUrl: './data-settings.component.html',
    styleUrl: './data-settings.component.scss'
})
export class DataSettingsComponent {
  private api = inject(ApiClient);
  private calendar = inject(CalendarService);
  private auth = inject(AuthService);
  private destroyRef = inject(DestroyRef);
  private document = inject(DOCUMENT);

  /** Downloaded one after another, in this order. */
  private readonly exports: ExportFile[] = [
    { name: 'nestwall-tasks.csv', fetch: () => this.api.download('/api/v1/export/tasks.csv') },
    { name: 'nestwall-points-ledger.csv', fetch: () => this.api.download('/api/v1/export/ledger.csv') },
    { name: 'nestwall-members.csv', fetch: () => this.api.download('/api/v1/export/members.csv') },
    { name: 'nestwall-family.ics', fetch: () => this.calendar.exportFamily() },
  ];

  permsReady = signal(false);
  /** Backup list, run, download, restore and reset (`backup.manage`; restore and reset check their own codes in the card). */
  canBackup = signal(false);
  /** The CSV and calendar export (`settings.manage`). */
  canExport = signal(false);
  exporting = signal(false);
  exportError = signal<string | null>(null);

  constructor() {
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.canBackup.set(this.auth.can('backup.manage'));
        this.canExport.set(this.auth.can('settings.manage'));
        this.permsReady.set(true);
      });
  }

  exportAll(): void {
    if (this.exporting()) return;
    this.exporting.set(true);
    this.exportError.set(null);
    from(this.exports)
      .pipe(
        concatMap((file) => file.fetch().pipe(map((blob) => ({ name: file.name, blob })))),
        finalize(() => this.exporting.set(false)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: ({ name, blob }) => this.save(name, blob),
        error: () => this.exportError.set('settings.data.export.failed'),
      });
  }

  private save(name: string, file: Blob): void {
    const url = URL.createObjectURL(file);
    const link = this.document.createElement('a');
    link.href = url;
    link.download = name;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url));
  }
}
