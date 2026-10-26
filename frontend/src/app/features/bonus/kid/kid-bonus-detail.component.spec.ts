import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap, provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { BonusClaim, BonusTask } from '../bonus.models';
import { BonusService } from '../bonus.service';
import { KidBonusDetailComponent } from './kid-bonus-detail.component';

function claim(over: Partial<BonusClaim> = {}): BonusClaim {
  return {
    id: 'c1',
    bonus_task_id: 'b1',
    family_member_id: 'm1',
    status: 'claimed',
    claimed_at: '2026-10-09T08:00:00Z',
    released_at: null,
    completed_at: null,
    decided_at: null,
    decided_by_member_id: null,
    decision_note: null,
    version: 2,
    ...over,
  };
}

function task(over: Partial<BonusTask> = {}): BonusTask {
  return {
    id: 'b1',
    template_id: null,
    title: 'Wash the car',
    description: 'Inside and out',
    points: 3,
    claim_mode: 'first_come',
    approval_required: false,
    day: '2026-10-09',
    show_from: null,
    ends_at: '2026-10-09T20:00:00Z',
    state: 'open',
    cancelled_at: null,
    version: 1,
    editable: false,
    eligible_member_ids: ['m1'],
    my_claim: null,
    taken_by_other: false,
    ...over,
  };
}

function el(fixture: ComponentFixture<KidBonusDetailComponent>, testId: string): HTMLElement | null {
  return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
}

describe('KidBonusDetailComponent', () => {
  let service: jasmine.SpyObj<BonusService>;

  async function create(
    id: string | null = 'b1',
    from: string | null = null,
  ): Promise<ComponentFixture<KidBonusDetailComponent>> {
    const queryParams: Record<string, string> = from ? { from } : {};
    const route = {
      paramMap: of(convertToParamMap(id ? { id } : {})),
      queryParamMap: of(convertToParamMap(queryParams)),
      snapshot: { paramMap: convertToParamMap(id ? { id } : {}), queryParamMap: convertToParamMap(queryParams) },
    };
    await TestBed.configureTestingModule({
      imports: [KidBonusDetailComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        { provide: BonusService, useValue: service },
        { provide: ActivatedRoute, useValue: route },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(KidBonusDetailComponent);
    fixture.detectChanges();
    return fixture;
  }

  beforeEach(() => {
    service = jasmine.createSpyObj<BonusService>('BonusService', ['list', 'claim', 'release', 'complete']);
    service.list.and.returnValue(of({ tasks: [task()] }));
    service.claim.and.returnValue(of(claim()));
    service.release.and.returnValue(of(claim({ status: 'released' })));
    service.complete.and.returnValue(of(claim({ status: 'submitted' })));
  });

  it('loads today and shows the card with its description and a grab button when open', async () => {
    const fixture = await create('b1');
    expect(service.list).toHaveBeenCalledWith('today');
    expect(fixture.nativeElement.querySelector('#bonus-title')?.textContent).toContain('Wash the car');
    expect(el(fixture, 'bonus-description')?.textContent).toContain('Inside and out');
    expect(el(fixture, 'bonus-grab')).not.toBeNull();
  });

  it('shows the no-description note when the task has no description', async () => {
    service.list.and.returnValue(of({ tasks: [task({ description: null })] }));
    const fixture = await create('b1');
    expect(el(fixture, 'bonus-description')?.textContent).toContain('No extra details for this bonus task.');
  });

  it('shows the not-found state when the card is not on today\'s list', async () => {
    service.list.and.returnValue(of({ tasks: [task({ id: 'other' })] }));
    const fixture = await create('b1');
    expect(el(fixture, 'bonus-missing')).not.toBeNull();
    expect(el(fixture, 'bonus-grab')).toBeNull();
  });

  it('shows the not-found state when there is no id in the route', async () => {
    const fixture = await create(null);
    expect(service.list).not.toHaveBeenCalled();
    expect(el(fixture, 'bonus-missing')).not.toBeNull();
  });

  it('shows a load error with a retry when the list fails', async () => {
    service.list.and.returnValue(throwError(() => new Error('down')));
    const fixture = await create('b1');
    expect(el(fixture, 'bonus-load-error')).not.toBeNull();
    expect(el(fixture, 'bonus-loading')).toBeNull();

    service.list.and.returnValue(of({ tasks: [task()] }));
    (el(fixture, 'bonus-load-error')?.querySelector('button') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(service.list).toHaveBeenCalledTimes(2);
    expect(el(fixture, 'bonus-load-error')).toBeNull();
    expect(el(fixture, 'bonus-missing')).toBeNull();
  });

  it('claims an open card and reloads it', async () => {
    const fixture = await create('b1');
    el(fixture, 'bonus-grab')?.click();
    expect(service.claim).toHaveBeenCalledWith('b1');
    expect(service.list).toHaveBeenCalledTimes(2);
    fixture.detectChanges();
  });

  it('shows the read-only taken notice for a first come card another child holds', async () => {
    service.list.and.returnValue(of({ tasks: [task({ taken_by_other: true })] }));
    const fixture = await create('b1');
    expect(el(fixture, 'bonus-taken')).not.toBeNull();
    expect(el(fixture, 'bonus-grab')).toBeNull();
    expect(el(fixture, 'bonus-finish')).toBeNull();
  });

  it('finishes and gives back the child\'s own claim with its version', async () => {
    service.list.and.returnValue(of({ tasks: [task({ my_claim: claim({ version: 5 }) })] }));
    const fixture = await create('b1');
    el(fixture, 'bonus-finish')?.click();
    expect(service.complete).toHaveBeenCalledWith('b1', 5);
    el(fixture, 'bonus-giveback')?.click();
    expect(service.release).toHaveBeenCalledWith('b1', 5);
  });

  it('shows the done notice for a submitted or approved claim', async () => {
    service.list.and.returnValue(of({ tasks: [task({ my_claim: claim({ status: 'submitted' }) })] }));
    const fixture = await create('b1');
    expect(el(fixture, 'bonus-done')).not.toBeNull();
    expect(el(fixture, 'bonus-finish')).toBeNull();
  });

  it('shows the error banner when an action fails', async () => {
    service.claim.and.returnValue(throwError(() => new Error('nope')));
    const fixture = await create('b1');
    el(fixture, 'bonus-grab')?.click();
    fixture.detectChanges();
    expect(el(fixture, 'bonus-error')).not.toBeNull();
  });

  it('sends back to the tab the bonus was opened from', async () => {
    const fixture = await create('b1', 'waiting');
    const back = el(fixture, 'bonus-back') as HTMLAnchorElement;
    expect(back.getAttribute('href')).toBe('/my-tasks?tab=waiting');
  });

  it('sends back to the Bonus tab when opened without a tab', async () => {
    const fixture = await create('b1');
    const back = el(fixture, 'bonus-back') as HTMLAnchorElement;
    expect(back.getAttribute('href')).toBe('/my-tasks?tab=bonus');
  });

  it('sends back to the Today page when opened from it', async () => {
    const fixture = await create('b1', 'home');
    const back = el(fixture, 'bonus-back') as HTMLAnchorElement;
    expect(back.getAttribute('href')).toBe('/today');
  });
});
