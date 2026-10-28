import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Router, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../../core/auth/auth.service';
import { ApiError } from '../../../core/api/api-client.service';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { RedemptionListItem } from '../rewards.models';
import { RewardsService } from '../rewards.service';

const PAGE_SIZE = 10;

interface Notice {
  key: string;
  params: Record<string, unknown>;
}

/** Every reward request the child has made, newest first, one page at a time. */
@Component({
    selector: 'app-reward-requests',
    imports: [DatePipe, RouterLink, TranslocoModule, BannerComponent, LoadErrorComponent, LoadingComponent],
    templateUrl: './reward-requests.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './reward-requests.component.scss'
})
export class RewardRequestsComponent implements OnInit {
  protected auth = inject(AuthService);
  private api = inject(RewardsService);
  private router = inject(Router);
  private destroyRef = inject(DestroyRef);

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);
  items = signal<RedemptionListItem[]>([]);
  nextCursor = signal<string | null>(null);
  pageIndex = signal(0);
  actionError = signal<string | null>(null);
  notice = signal<Notice | null>(null);

  cancelTarget = signal<RedemptionListItem | null>(null);
  cancelBusy = signal(false);

  private requestSeq = 0;
  /** Cursor that starts each page, indexed by page. Page 0 starts from the newest request. */
  private pageCursors: (string | null)[] = [null];

  ngOnInit(): void {
    // The parent's view of the rewards route is the catalogue, so a parent goes back there.
    if (!this.auth.isChild()) {
      this.router.navigateByUrl('/rewards');
      return;
    }
    this.fetch();
  }

  nextPage(): void {
    if (!this.nextCursor() || this.loadState() === 'loading') return;
    this.pageIndex.update((p) => p + 1);
    this.fetch();
  }

  prevPage(): void {
    if (this.pageIndex() === 0 || this.loadState() === 'loading') return;
    this.pageIndex.update((p) => p - 1);
    this.fetch();
  }

  /** Loads the current page. Responses from an older request are ignored. */
  fetch(): void {
    const seq = ++this.requestSeq;
    if (this.loadState() !== 'ready') this.loadState.set('loading');
    this.loadError.set(null);
    const cursor = this.pageCursors[this.pageIndex()] ?? undefined;
    this.api
      .redemptionPage({ limit: PAGE_SIZE, cursor })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          if (seq !== this.requestSeq) return;
          this.items.set(page.redemptions);
          this.nextCursor.set(page.next_cursor);
          if (page.next_cursor) this.pageCursors[this.pageIndex() + 1] = page.next_cursor;
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          if (seq !== this.requestSeq) return;
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  openCancel(item: RedemptionListItem): void {
    this.cancelTarget.set(item);
  }

  closeCancel(): void {
    if (!this.cancelBusy()) this.cancelTarget.set(null);
  }

  confirmCancel(): void {
    const target = this.cancelTarget();
    if (!target || this.cancelBusy()) return;
    this.cancelBusy.set(true);
    this.actionError.set(null);
    this.api
      .cancel(target.id, target.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.cancelBusy.set(false);
          this.cancelTarget.set(null);
          this.showNotice({ key: 'rewards.cancel.done', params: { count: target.cost } });
          this.fetch();
        },
        error: (err: unknown) => {
          this.cancelBusy.set(false);
          this.cancelTarget.set(null);
          this.actionError.set(this.errorKey(err));
          this.fetch();
        },
      });
  }

  dismissNotice(): void {
    this.notice.set(null);
  }

  private showNotice(notice: Notice): void {
    this.notice.set(notice);
    setTimeout(() => {
      if (this.notice() === notice) this.notice.set(null);
    }, 3200);
  }

  /** Maps an API error code to an i18n key. Unknown codes use the generic message. */
  private errorKey(err: unknown): string {
    const code = (err as { code?: string } | null)?.code ?? '';
    return code === 'not_pending' ? 'rewards.error.not_pending' : 'rewards.error.generic';
  }
}
