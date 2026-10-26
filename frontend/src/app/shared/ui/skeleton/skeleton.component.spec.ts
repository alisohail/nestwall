import { ComponentFixture, TestBed } from '@angular/core/testing';
import { SkeletonComponent } from './skeleton.component';

describe('SkeletonComponent', () => {
  let fixture: ComponentFixture<SkeletonComponent>;
  let el: HTMLElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [SkeletonComponent] }).compileComponents();
    fixture = TestBed.createComponent(SkeletonComponent);
    el = fixture.nativeElement as HTMLElement;
  });

  it('renders three rows by default and hides them from assistive tech', () => {
    fixture.detectChanges();
    expect(el.querySelectorAll('.nw-skeleton__row').length).toBe(3);
    expect(el.querySelector('.nw-skeleton')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('renders the requested number of rows', () => {
    fixture.componentRef.setInput('rows', 5);
    fixture.detectChanges();
    expect(el.querySelectorAll('.nw-skeleton__row').length).toBe(5);
  });
});
