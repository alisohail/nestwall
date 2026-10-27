import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { ApiClient } from '../../core/api/api-client.service';
import { AuthService } from '../../core/auth/auth.service';
import { InboxItem, NotificationBellComponent, panelPlacement } from './notification-bell.component';

describe('panelPlacement', () => {
  it('should right-align the panel to the bell on a wide screen', () => {
    expect(panelPlacement({ bottom: 60, right: 1200 }, 1280)).toEqual({ top: 68, left: 860, width: 340 });
  });

  it('should keep the panel inside the left edge when the bell is near the middle of a phone', () => {
    const placed = panelPlacement({ bottom: 64, right: 280 }, 412);
    expect(placed.left).toBe(8);
    expect(placed.width).toBe(340);
    expect(placed.left + placed.width).toBeLessThanOrEqual(412 - 8);
  });

  it('should narrow the panel on a very small screen', () => {
    const placed = panelPlacement({ bottom: 64, right: 300 }, 320);
    expect(placed.width).toBe(304);
    expect(placed.left).toBe(8);
  });
});

const ITEMS: InboxItem[] = [
  { id: 'n1', kind: 'task_approved', subject_member_id: null, body: 'Tidy room was approved', created_at: '2026-10-20T10:00:00Z', read_at: null },
  { id: 'n2', kind: 'bonus_approved', subject_member_id: null, body: 'Bonus approved', created_at: '2026-10-19T10:00:00Z', read_at: '2026-10-19T11:00:00Z' },
];

/** Stands in for EventSource so tests can fire the server's events. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  closed = false;
  private listeners: Record<string, () => void> = {};

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: () => void): void {
    this.listeners[type] = fn;
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string): void {
    this.listeners[type]?.();
  }
}

describe('NotificationBellComponent', () => {
  let fixture: ComponentFixture<NotificationBellComponent>;
  let api: jasmine.SpyObj<ApiClient>;
  let originalEventSource: typeof EventSource;

  beforeEach(() => {
    originalEventSource = window.EventSource;
    FakeEventSource.instances = [];
    (window as unknown as { EventSource: unknown }).EventSource = FakeEventSource;

    api = jasmine.createSpyObj<ApiClient>('ApiClient', ['get', 'post', 'delete']);
    api.get.and.returnValue(of({ notifications: ITEMS, unread_count: 1, browser_enabled: false }));
    api.post.and.returnValue(of({}));
    api.delete.and.returnValue(of(undefined));

    TestBed.configureTestingModule({
      imports: [NotificationBellComponent, getTranslocoTestingModule()],
      providers: [provideRouter([]), { provide: ApiClient, useValue: api }, { provide: AuthService, useValue: { can: () => true, isChild: () => false } }],
    });
    fixture = TestBed.createComponent(NotificationBellComponent);
    fixture.detectChanges();
  });

  afterEach(() => {
    (window as unknown as { EventSource: unknown }).EventSource = originalEventSource;
  });

  it('opens the live stream and refreshes when the server reports a change', () => {
    expect(FakeEventSource.instances.length).toBe(1);
    expect(FakeEventSource.instances[0].url).toBe('/api/v1/notifications/stream');

    api.get.and.returnValue(of({ notifications: [ITEMS[1]], unread_count: 0, browser_enabled: false }));
    FakeEventSource.instances[0].emit('changed');

    expect(fixture.componentInstance.items().length).toBe(1);
    expect(fixture.componentInstance.unread()).toBe(0);
  });

  it('closes the live stream when the bell is destroyed', () => {
    fixture.destroy();
    expect(FakeEventSource.instances[0].closed).toBe(true);
  });

  it('marks unread items with a New tag and a tone-coloured kind icon', () => {
    fixture.componentInstance.toggle();
    fixture.detectChanges();
    const el: HTMLElement = fixture.nativeElement;

    expect(el.querySelectorAll('.bell-item.unread .bell-new').length).toBe(1);
    expect(el.querySelector('.bell-item.unread .bell-new')).not.toBeNull();
    expect(el.querySelector('[data-kind="bonus_approved"] .bell-new')).toBeNull();
    expect(el.querySelector('[data-kind="task_approved"] .bell-icon')?.classList).toContain('tone-success');
  });

  it('maps rejections to the error tone and unknown kinds to a neutral fallback', () => {
    const c = fixture.componentInstance;
    expect(c.kindMeta('task_rejected').tone).toBe('error');
    expect(c.kindMeta('task_approved').tone).toBe('success');
    expect(c.kindMeta('something_new')).toEqual({ icon: 'bell', tone: 'neutral' });
    expect(c.kindLabelKey('something_new')).toBe('shell.notif.kind.other');
    expect(c.kindLabelKey('task_approved')).toBe('shell.notif.kind.task_approved');
  });

  it('shows the unread count from the server', () => {
    const badge: HTMLElement | null = fixture.nativeElement.querySelector('[data-testid="bell-count"]');
    expect(badge?.textContent?.trim()).toBe('1');
    expect(api.get).toHaveBeenCalledWith('/api/v1/notifications');
  });

  it('opens the panel on click and lists recent notifications', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('[data-testid="bell-panel"]')).toBeNull();

    el.querySelector<HTMLButtonElement>('[data-testid="bell-button"]')?.click();
    fixture.detectChanges();

    expect(el.querySelector('[data-testid="bell-panel"]')).not.toBeNull();
    expect(el.querySelectorAll('.bell-list li').length).toBe(2);
  });

  it('marks an unread item read and lowers the count', () => {
    fixture.componentInstance.toggle();
    fixture.detectChanges();

    fixture.componentInstance.markRead(ITEMS[0]);

    expect(api.post).toHaveBeenCalledWith('/api/v1/notifications/n1/read', {}, undefined, undefined, { silent: true });
    expect(fixture.componentInstance.unread()).toBe(0);
  });

  it('clears every notification from the panel', () => {
    fixture.componentInstance.toggle();
    fixture.detectChanges();

    fixture.componentInstance.clearAll();

    expect(api.delete).toHaveBeenCalledWith('/api/v1/notifications');
    expect(fixture.componentInstance.items().length).toBe(0);
    expect(fixture.componentInstance.unread()).toBe(0);
  });

  describe('browser alerts', () => {
    let originalNotification: typeof Notification;
    let created: jasmine.Spy;

    beforeEach(() => {
      originalNotification = window.Notification;
      created = jasmine.createSpy('Notification');
      class FakeNotification {
        static permission: NotificationPermission = 'granted';
        static requestPermission = () => Promise.resolve<NotificationPermission>('granted');
        constructor(title: string, opts?: NotificationOptions) {
          created(title, opts);
        }
      }
      (window as unknown as { Notification: unknown }).Notification = FakeNotification;
    });

    afterEach(() => {
      (window as unknown as { Notification: unknown }).Notification = originalNotification;
    });

    it('does not alert for notices that already existed on first load', () => {
      api.get.and.returnValue(of({ notifications: ITEMS, unread_count: 1, browser_enabled: true }));
      TestBed.resetTestingModule();
      TestBed.configureTestingModule({
        imports: [NotificationBellComponent, getTranslocoTestingModule()],
        providers: [provideRouter([]), { provide: ApiClient, useValue: api }, { provide: AuthService, useValue: { can: () => true, isChild: () => false } }],
      });
      const f = TestBed.createComponent(NotificationBellComponent);
      f.detectChanges();

      expect(created).not.toHaveBeenCalled();
    });

    it('alerts once for a new unread notice when the family has browser on', () => {
      api.get.and.returnValue(of({ notifications: ITEMS, unread_count: 1, browser_enabled: true }));
      TestBed.resetTestingModule();
      TestBed.configureTestingModule({
        imports: [NotificationBellComponent, getTranslocoTestingModule()],
        providers: [provideRouter([]), { provide: ApiClient, useValue: api }, { provide: AuthService, useValue: { can: () => true, isChild: () => false } }],
      });
      const f = TestBed.createComponent(NotificationBellComponent);
      f.detectChanges();

      const fresh: InboxItem = { ...ITEMS[0], id: 'n3', body: 'Redemption approved' };
      api.get.and.returnValue(of({ notifications: [fresh, ...ITEMS], unread_count: 2, browser_enabled: true }));
      f.componentInstance.refresh();

      expect(created).toHaveBeenCalledTimes(1);
      expect(created.calls.mostRecent().args[1]).toEqual(jasmine.objectContaining({ body: 'Redemption approved', tag: 'n3' }));
    });

    it('offers an enable button while permission is undecided, and hides it once granted', async () => {
      (window.Notification as unknown as { permission: string }).permission = 'default';
      (window.Notification as unknown as { requestPermission: () => Promise<string> }).requestPermission = () => {
        (window.Notification as unknown as { permission: string }).permission = 'granted';
        return Promise.resolve('granted');
      };
      api.get.and.returnValue(of({ notifications: ITEMS, unread_count: 1, browser_enabled: true }));
      TestBed.resetTestingModule();
      TestBed.configureTestingModule({
        imports: [NotificationBellComponent, getTranslocoTestingModule()],
        providers: [provideRouter([]), { provide: ApiClient, useValue: api }, { provide: AuthService, useValue: { can: () => true, isChild: () => false } }],
      });
      const f = TestBed.createComponent(NotificationBellComponent);
      f.detectChanges();
      f.componentInstance.toggle();
      f.detectChanges();

      const button = f.nativeElement.querySelector('[data-testid="bell-enable-alerts"]') as HTMLButtonElement;
      expect(button).not.toBeNull();

      button.click();
      await new Promise((resolve) => setTimeout(resolve));
      f.detectChanges();

      expect(f.componentInstance.permission()).toBe('granted');
      expect(f.nativeElement.querySelector('[data-testid="bell-enable-alerts"]')).toBeNull();
    });
  });
});
