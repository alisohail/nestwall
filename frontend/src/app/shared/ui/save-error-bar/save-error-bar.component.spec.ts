import { ComponentFixture, TestBed } from '@angular/core/testing';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { BusyService } from '../../../core/busy/busy.service';
import { SaveErrorBarComponent } from './save-error-bar.component';

describe('SaveErrorBarComponent', () => {
  let fixture: ComponentFixture<SaveErrorBarComponent>;
  let busy: BusyService;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SaveErrorBarComponent, getTranslocoTestingModule()],
    }).compileComponents();
    fixture = TestBed.createComponent(SaveErrorBarComponent);
    busy = TestBed.inject(BusyService);
  });

  it('is hidden when there is no failed save', () => {
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="save-error-bar"]')).toBeNull();
  });

  it('shows the failure message and hides when dismissed', () => {
    busy.saveFailure.set({ message: 'Email already taken' });
    fixture.detectChanges();

    const bar: HTMLElement = fixture.nativeElement.querySelector('[data-testid="save-error-bar"]');
    expect(bar.textContent).toContain('Email already taken');

    bar.querySelector<HTMLButtonElement>('button')!.click();
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[data-testid="save-error-bar"]')).toBeNull();
  });
});
