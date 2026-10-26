import { ComponentFixture, TestBed } from '@angular/core/testing';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AvatarComponent } from './avatar.component';
import { AvatarService } from './avatar.service';

describe('AvatarComponent', () => {
  let fixture: ComponentFixture<AvatarComponent>;
  let avatarSpy: jasmine.SpyObj<AvatarService>;

  beforeEach(async () => {
    avatarSpy = jasmine.createSpyObj<AvatarService>('AvatarService', ['url']);
    avatarSpy.url.and.callFake((id: string, size: 96 | 256 = 96) => `/api/v1/avatars/${id}?size=${size}`);

    await TestBed.configureTestingModule({
      imports: [AvatarComponent, getTranslocoTestingModule()],
      providers: [{ provide: AvatarService, useValue: avatarSpy }],
    }).compileComponents();

    fixture = TestBed.createComponent(AvatarComponent);
    fixture.componentRef.setInput('name', 'Sam');
    fixture.componentRef.setInput('colour', '#6366f1');
  });

  it('should show the initial on the member colour when there is no picture', () => {
    fixture.detectChanges();
    const letter = fixture.nativeElement.querySelector('span.avatar') as HTMLElement;
    expect(letter.textContent?.trim()).toBe('S');
    expect(letter.style.background).toContain('rgb(99, 102, 241)');
    expect(fixture.nativeElement.querySelector('img')).toBeNull();
  });

  it('should show the picture at the small size by default', () => {
    fixture.componentRef.setInput('avatarId', 'a1');
    fixture.detectChanges();
    const img = fixture.nativeElement.querySelector('img') as HTMLImageElement;
    expect(img).toBeTruthy();
    expect(img.getAttribute('src')).toBe('/api/v1/avatars/a1?size=96');
    expect(img.alt).toContain('Sam');
  });

  it('should request the large size for the xl variant', () => {
    fixture.componentRef.setInput('avatarId', 'a1');
    fixture.componentRef.setInput('size', 'xl');
    fixture.detectChanges();
    const img = fixture.nativeElement.querySelector('img') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/v1/avatars/a1?size=256');
  });
});
