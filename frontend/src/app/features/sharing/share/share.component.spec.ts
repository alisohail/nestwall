import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { ShareSummary } from '../sharing.models';
import { SharingService } from '../sharing.service';
import { ShareComponent } from './share.component';

function sample(over: Partial<ShareSummary> = {}): ShareSummary {
  return {
    enabled: true,
    min_per_share: 1,
    max_per_share: 30,
    daily_limit: 50,
    min_balance: 10,
    approval_mode: 'never',
    approval_above: 0,
    notify_recipient: true,
    notify_parents: true,
    version: 1,
    balance: 182,
    shared_today: 0,
    daily_left: 50,
    notes: ['thank_you', 'happy_birthday', 'good_job', 'just_because'],
    recipients: [{ member_id: 'm2', name: 'Ava', colour: '#f472b6', avatar_id: null }],
    ...over,
  };
}

describe('ShareComponent', () => {
  let fixture: ComponentFixture<ShareComponent>;
  let sharing: jasmine.SpyObj<SharingService>;

  async function create(): Promise<void> {
    fixture = TestBed.createComponent(ShareComponent);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function el(testId: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
  }

  beforeEach(async () => {
    sharing = jasmine.createSpyObj<SharingService>('SharingService', ['summary', 'share']);
    sharing.summary.and.returnValue(of(sample()));

    await TestBed.configureTestingModule({
      imports: [ShareComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: SharingService, useValue: sharing },
        { provide: AuthService, useValue: { isChild: () => true } },
      ],
    }).compileComponents();
  });

  it('shows the load error screen and retries the summary when it cannot load', async () => {
    sharing.summary.and.returnValue(throwError(() => ({ code: 'network', message: 'offline' })));
    await create();
    expect(el('share-loading')).toBeNull();
    expect(fixture.nativeElement.querySelector('app-load-error')).not.toBeNull();
    sharing.summary.and.returnValue(of(sample()));
    (el('load-error-retry') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(sharing.summary).toHaveBeenCalledTimes(2);
    expect(el('share-balance')?.textContent).toContain('182');
  });

  it('shows the form when sharing is on', async () => {
    await create();
    expect(el('share-balance')?.textContent).toContain('182');
    expect(el('share-submit')).not.toBeNull();
    expect(el('share-off')).toBeNull();
  });

  it('shows only the off message and no content when sharing is disabled', async () => {
    sharing.summary.and.returnValue(of(sample({ enabled: false })));
    await create();
    expect(el('share-off')?.textContent).toContain('Sharing is turned off');
    expect(el('share-balance')).toBeNull();
    expect(el('share-amount')).toBeNull();
    expect(el('share-submit')).toBeNull();
    expect(fixture.nativeElement.querySelector('.hero')).toBeNull();
    expect(fixture.nativeElement.querySelector('.picks')).toBeNull();
    expect(fixture.nativeElement.querySelector('.summary')).toBeNull();
  });
});
