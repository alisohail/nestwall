import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ThemeService } from '../../../core/theme/theme.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { LoadingComponent } from './loading.component';

describe('LoadingComponent', () => {
  let fixture: ComponentFixture<LoadingComponent>;
  let el: HTMLElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [LoadingComponent, getTranslocoTestingModule()],
    }).compileComponents();
    fixture = TestBed.createComponent(LoadingComponent);
    el = fixture.nativeElement as HTMLElement;
  });

  it('renders the inline variant with a decorative mark and visible label', () => {
    fixture.detectChanges();
    const mini = el.querySelector('.nw-loading__mini') as HTMLImageElement;
    expect(mini.getAttribute('alt')).toBe('');
    expect(el.querySelector('.nw-loading__label')?.textContent?.trim()).toBe('Loading...');
  });

  it('renders the full variant with an accessible logo', () => {
    fixture.componentRef.setInput('variant', 'full');
    fixture.detectChanges();
    const logo = el.querySelector('.nw-loading__logo') as HTMLImageElement;
    expect(logo.getAttribute('alt')).toBe('Nesty, loading');
    expect(logo.getAttribute('src')).toBe('/assets/images/nesty.svg');
    expect(el.querySelector('[role="status"]')).not.toBeNull();
  });

  it('uses the dark logo in dark theme', () => {
    TestBed.inject(ThemeService).currentTheme.set('dark');
    fixture.componentRef.setInput('variant', 'full');
    fixture.detectChanges();
    const logo = el.querySelector('.nw-loading__logo') as HTMLImageElement;
    expect(logo.getAttribute('src')).toBe('/assets/images/nesty-dark.svg');
  });

  it('renders the spinner variant with a screen reader label', () => {
    fixture.componentRef.setInput('variant', 'spinner');
    fixture.componentRef.setInput('labelKey', 'loading.default');
    fixture.detectChanges();
    expect(el.querySelector('.nw-loading__ring')?.getAttribute('aria-hidden')).toBe('true');
    expect(el.querySelector('.nw-sr-only')?.textContent?.trim()).toBe('Loading...');
  });
});
