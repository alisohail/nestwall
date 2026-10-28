import { DatePipe } from '@angular/common';
import { Component, DestroyRef, Input, OnInit, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Router } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { LoadErrorComponent } from '../../shared/ui/load-error/load-error.component';
import { Observable, switchMap } from 'rxjs';
import { ApiError } from '../../core/api/api-client.service';
import { AuthService } from '../../core/auth/auth.service';
import { TimeFormatService } from '../../core/time-format/time-format.service';
import { SessionView, SessionsService } from './sessions.service';

@Component({
  selector: 'app-sessions',
  standalone: true,
  imports: [DatePipe, TranslocoModule, LoadingComponent, LoadErrorComponent],
  templateUrl: './sessions.component.html',
  styleUrl: './sessions.component.scss',
})
export class SessionsComponent implements OnInit {
  /** Set by a parent to manage a child's sessions. Null shows the signed-in user's own sessions. */
  @Input() memberId: string | null = null;
  @Input() memberName = '';

  private sessions = inject(SessionsService);
  private auth = inject(AuthService);
  timeFormat = inject(TimeFormatService);
  private router = inject(Router);
  private destroyRef = inject(DestroyRef);

  list = signal<SessionView[]>([]);
  loading = signal(true);
  loadError = signal<ApiError | null>(null);
  actionError = signal<string | null>(null);
  busy = signal(false);
  confirmingAll = signal(false);

  ngOnInit(): void {
    this.load();
  }

  get isOwn(): boolean {
    return this.memberId === null;
  }

  get headingId(): string {
    return `sessions-heading-${this.memberId ?? 'own'}`;
  }

  load(): void {
    this.loading.set(true);
    this.loadError.set(null);
    const memberId = this.memberId;
    const request = memberId === null ? this.sessions.listOwn() : this.sessions.listForMember(memberId);
    request.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      next: (res) => {
        this.list.set(res.sessions);
        this.loading.set(false);
      },
      error: (err: ApiError) => {
        this.loadError.set(err);
        this.loading.set(false);
      },
    });
  }

  revoke(session: SessionView): void {
    this.run(this.sessions.revokeOwn(session.id), () =>
      this.list.update((items) => items.filter((s) => s.id !== session.id))
    );
  }

  signOutOthers(): void {
    this.run(this.sessions.revokeOthers(), () => this.load());
  }

  signOutEverywhere(): void {
    const memberId = this.memberId;
    if (memberId === null) {
      const everywhere: Observable<void> = this.sessions
        .revokeOthers()
        .pipe(switchMap(() => this.auth.logout()));
      this.run(everywhere, () => void this.router.navigate(['/login']));
      return;
    }
    this.run(this.sessions.revokeAllForMember(memberId), () => {
      this.confirmingAll.set(false);
      this.load();
    });
  }

  private run(action: Observable<void>, onDone: () => void): void {
    this.busy.set(true);
    this.actionError.set(null);
    action.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      next: () => {
        this.busy.set(false);
        onDone();
      },
      error: () => {
        this.busy.set(false);
        this.actionError.set('sessions.error.action');
      },
    });
  }
}
