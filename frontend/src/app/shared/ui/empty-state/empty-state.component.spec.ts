import { ComponentFixture, TestBed } from '@angular/core/testing';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { EmptyStateComponent } from './empty-state.component';

describe('EmptyStateComponent', () => {
  let fixture: ComponentFixture<EmptyStateComponent>;
  let el: HTMLElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [EmptyStateComponent, getTranslocoTestingModule()],
    }).compileComponents();
    fixture = TestBed.createComponent(EmptyStateComponent);
    el = fixture.nativeElement as HTMLElement;
  });

  it('renders the translated title', () => {
    fixture.componentRef.setInput('titleKey', 'errors.not_found.title');
    fixture.detectChanges();
    expect(el.querySelector('.nw-empty__title')?.textContent?.trim()).toBe("We can't find that page");
  });

  it('renders the body only when a body key is given', () => {
    fixture.componentRef.setInput('titleKey', 'errors.not_found.title');
    fixture.detectChanges();
    expect(el.querySelector('.nw-empty__body')).toBeNull();

    fixture.componentRef.setInput('bodyKey', 'errors.server.body');
    fixture.detectChanges();
    expect(el.querySelector('.nw-empty__body')?.textContent).toContain('Sorry about that');
  });
});
