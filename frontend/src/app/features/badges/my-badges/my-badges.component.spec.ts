import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { MyBadges } from '../badges.models';
import { BadgesService } from '../badges.service';
import { MyBadgesComponent } from './my-badges.component';

function sample(over: Partial<MyBadges> = {}): MyBadges {
  return {
    enabled: true,
    show_on_kid_dashboard: true,
    earned_count: 2,
    total_count: 24,
    groups: [
      {
        group: 'streaks',
        earned: 1,
        total: 2,
        badges: [
          { key: 's3', earned: true, earned_at: '2026-10-01T08:00:00Z', progress: 3, target: 3 },
          { key: 's7', earned: false, earned_at: null, progress: 2, target: 7 },
        ],
      },
    ],
    from_parents: [
      { id: 'a1', name: 'Helper', emoji: '🌟', note: 'Helped with homework', awarded_by: 'Mum', awarded_at: '2026-10-02T08:00:00Z' },
    ],
    ...over,
  };
}

describe('MyBadgesComponent', () => {
  let fixture: ComponentFixture<MyBadgesComponent>;
  let badges: jasmine.SpyObj<BadgesService>;

  async function create(): Promise<void> {
    fixture = TestBed.createComponent(MyBadgesComponent);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function el(testId: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
  }

  beforeEach(async () => {
    badges = jasmine.createSpyObj<BadgesService>('BadgesService', ['mine']);
    badges.mine.and.returnValue(of(sample()));

    await TestBed.configureTestingModule({
      imports: [MyBadgesComponent, getTranslocoTestingModule()],
      providers: [provideRouter([]), { provide: BadgesService, useValue: badges }],
    }).compileComponents();
  });

  it('shows the count line, the parent badges and one section per group', async () => {
    await create();
    expect(el('my-badges-page')).not.toBeNull();
    expect(el('my-badges-count')?.textContent).toContain('2');
    expect(el('my-badges-from-parents')?.textContent).toContain('Helper');
    expect(el('my-badges-group-streaks')?.querySelectorAll('.tile').length).toBe(2);
  });

  it('marks an earned badge as earned and an unearned one with progress', async () => {
    await create();
    const group = el('my-badges-group-streaks') as HTMLElement;
    const tiles = group.querySelectorAll('.tile');
    expect(tiles[0].classList.contains('locked')).toBeFalse();
    expect(tiles[0].querySelector('.earned')).not.toBeNull();
    expect(tiles[1].classList.contains('locked')).toBeTrue();
    expect(tiles[1].querySelector('.prog')?.textContent).toContain('2');
    expect(tiles[1].querySelector('.bp i')?.getAttribute('style')).toContain('%');
  });

  it('shows the switched-off state and no badges when badges are off', async () => {
    badges.mine.and.returnValue(of(sample({ enabled: false, groups: [], from_parents: [] })));
    await create();
    expect(el('my-badges-off')).not.toBeNull();
    expect(el('my-badges-group-streaks')).toBeNull();
  });

  it('shows the empty state when there is nothing to show', async () => {
    badges.mine.and.returnValue(of(sample({ groups: [], from_parents: [], earned_count: 0 })));
    await create();
    expect(el('my-badges-empty')).not.toBeNull();
  });

  it('shows an error with a retry that loads the page', async () => {
    badges.mine.and.returnValue(throwError(() => ({ status: 500 })));
    await create();
    expect(el('my-badges-error')).not.toBeNull();

    badges.mine.and.returnValue(of(sample()));
    (el('my-badges-error')?.querySelector('button') as HTMLButtonElement).click();
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();

    expect(el('my-badges-error')).toBeNull();
    expect(el('my-badges-group-streaks')).not.toBeNull();
  });
});
