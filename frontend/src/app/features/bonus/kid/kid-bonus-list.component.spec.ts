import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { BonusClaim, BonusTask } from '../bonus.models';
import { BonusService } from '../bonus.service';
import { KidBonusListComponent, kidBonusState } from './kid-bonus-list.component';

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
    description: null,
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

function el(fixture: ComponentFixture<KidBonusListComponent>, testId: string): HTMLElement | null {
  return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
}

function cards(fixture: ComponentFixture<KidBonusListComponent>): HTMLElement[] {
  return Array.from(fixture.nativeElement.querySelectorAll('[data-testid="kid-bonus-card"]'));
}

describe('kidBonusState', () => {
  it('maps a live claim of the child to mine', () => {
    expect(kidBonusState(task({ my_claim: claim({ status: 'claimed' }) }))).toBe('mine');
  });

  it('maps a submitted or approved claim to done', () => {
    expect(kidBonusState(task({ my_claim: claim({ status: 'submitted' }) }))).toBe('done');
    expect(kidBonusState(task({ my_claim: claim({ status: 'approved' }) }))).toBe('done');
  });

  it('maps a first come task another child holds to taken', () => {
    expect(kidBonusState(task({ taken_by_other: true }))).toBe('taken');
  });

  it('maps an untaken task to open for first come and once for everyone once', () => {
    expect(kidBonusState(task({ claim_mode: 'first_come' }))).toBe('open');
    expect(kidBonusState(task({ claim_mode: 'everyone_once' }))).toBe('once');
  });
});

describe('KidBonusListComponent', () => {
  let service: jasmine.SpyObj<BonusService>;

  async function create(mode: 'section' | 'tab' = 'section'): Promise<ComponentFixture<KidBonusListComponent>> {
    await TestBed.configureTestingModule({
      imports: [KidBonusListComponent, getTranslocoTestingModule()],
      providers: [
        { provide: BonusService, useValue: service },
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(KidBonusListComponent);
    fixture.componentRef.setInput('mode', mode);
    fixture.detectChanges();
    return fixture;
  }

  beforeEach(() => {
    service = jasmine.createSpyObj<BonusService>('BonusService', ['list', 'claim', 'release', 'complete']);
    service.list.and.returnValue(of({ tasks: [] }));
  });

  it('shows nothing in section mode when there are no tasks', async () => {
    const fixture = await create('section');
    expect(service.list).toHaveBeenCalledWith('today');
    expect(el(fixture, 'kid-bonus-count')).toBeNull();
    expect(cards(fixture).length).toBe(0);
  });

  it('shows the empty message in tab mode when there are no tasks', async () => {
    const fixture = await create('tab');
    expect(el(fixture, 'kid-bonus-empty')).not.toBeNull();
  });

  it('shows a card per listed task with its state', async () => {
    service.list.and.returnValue(
      of({
        tasks: [
          task({ id: 'b1' }),
          task({ id: 'b2', taken_by_other: true, title: 'Taken' }),
          task({ id: 'b3', state: 'expired', title: 'Ended' }),
        ],
      }),
    );
    const fixture = await create('section');
    const rendered = cards(fixture);
    expect(rendered.length).toBe(2);
    expect(rendered[0].getAttribute('data-state')).toBe('open');
    expect(rendered[1].getAttribute('data-state')).toBe('taken');
    expect(el(fixture, 'kid-bonus-count')?.textContent).toContain('1');
  });

  it('links every card title to its detail page, including taken cards, and remembers the tab', async () => {
    service.list.and.returnValue(of({ tasks: [task({ id: 'b1' }), task({ id: 'b2', taken_by_other: true })] }));
    const fixture = await create('section');
    const links = Array.from(fixture.nativeElement.querySelectorAll('[data-testid="kid-bonus-title"]')) as HTMLAnchorElement[];
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['/my-tasks/bonus/b1?from=home', '/my-tasks/bonus/b2?from=home']);
  });

  it('returns from a bonus card on the Bonus tab to the Bonus tab', async () => {
    service.list.and.returnValue(of({ tasks: [task({ id: 'b1' })] }));
    const fixture = await create('tab');
    const link = fixture.nativeElement.querySelector('[data-testid="kid-bonus-title"]') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/my-tasks/bonus/b1?from=bonus');
  });

  it('labels an end-of-day task as until the end of the day and a timed task with its end time', async () => {
    const endOfDay = new Date(2026, 9, 10, 0, 0).toISOString();
    const timed = new Date(2026, 9, 9, 20, 0).toISOString();
    service.list.and.returnValue(
      of({ tasks: [task({ id: 'b1', ends_at: endOfDay }), task({ id: 'b2', ends_at: timed })] }),
    );
    const fixture = await create('section');
    const chips = cards(fixture).map((card) => card.querySelector('.meta .muted')?.textContent?.trim());
    expect(chips[0]).toBe('Until the end of the day');
    expect(chips[1]).toContain('Ends');
    expect(chips[1]).not.toContain('Until the end of the day');
  });

  it('reports the number of open cards', async () => {
    service.list.and.returnValue(of({ tasks: [task({ id: 'b1' }), task({ id: 'b2', taken_by_other: true })] }));
    const emitted: number[] = [];
    await TestBed.configureTestingModule({
      imports: [KidBonusListComponent, getTranslocoTestingModule()],
      providers: [
        { provide: BonusService, useValue: service },
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    const created = TestBed.createComponent(KidBonusListComponent);
    created.componentRef.setInput('mode', 'tab');
    created.componentInstance.openCount.subscribe((n: number) => emitted.push(n));
    created.detectChanges();
    expect(emitted).toEqual([1]);
  });

  it('claims an open task and reloads the list', async () => {
    service.list.and.returnValues(of({ tasks: [task()] }), of({ tasks: [task({ my_claim: claim() })] }));
    service.claim.and.returnValue(of(claim()));
    const fixture = await create('tab');
    (el(fixture, 'kid-bonus-grab') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(service.claim).toHaveBeenCalledWith('b1');
    expect(service.list).toHaveBeenCalledTimes(2);
    expect(cards(fixture)[0].getAttribute('data-state')).toBe('mine');
  });

  it('gives back a claim with its version', async () => {
    service.list.and.returnValues(of({ tasks: [task({ my_claim: claim({ version: 4 }) })] }), of({ tasks: [task()] }));
    service.release.and.returnValue(of(claim({ status: 'released' })));
    const fixture = await create('tab');
    (el(fixture, 'kid-bonus-giveback') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(service.release).toHaveBeenCalledWith('b1', 4);
  });

  it('marks a claim done with its version', async () => {
    service.list.and.returnValues(
      of({ tasks: [task({ my_claim: claim({ version: 5 }) })] }),
      of({ tasks: [task({ my_claim: claim({ status: 'submitted', version: 6 }) })] }),
    );
    service.complete.and.returnValue(of(claim({ status: 'submitted', version: 6 })));
    const fixture = await create('tab');
    (el(fixture, 'kid-bonus-finish') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(service.complete).toHaveBeenCalledWith('b1', 5);
    expect(cards(fixture)[0].getAttribute('data-state')).toBe('done');
  });

  it('shows the error and reloads when the task was taken first', async () => {
    service.list.and.returnValues(of({ tasks: [task()] }), of({ tasks: [task({ taken_by_other: true })] }));
    service.claim.and.returnValue(throwError(() => ({ code: 'taken', status: 409 })));
    const fixture = await create('tab');
    (el(fixture, 'kid-bonus-grab') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'kid-bonus-error')?.textContent).toContain('Another child');
    expect(cards(fixture)[0].getAttribute('data-state')).toBe('taken');
  });

  it('shows a load error with a retry', async () => {
    service.list.and.returnValue(throwError(() => ({ status: 500 })));
    const fixture = await create('section');
    expect(el(fixture, 'kid-bonus-load-error')).not.toBeNull();
    service.list.and.returnValue(of({ tasks: [task()] }));
    (el(fixture, 'kid-bonus-load-error')?.querySelector('button') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'kid-bonus-load-error')).toBeNull();
    expect(cards(fixture).length).toBe(1);
  });
});
