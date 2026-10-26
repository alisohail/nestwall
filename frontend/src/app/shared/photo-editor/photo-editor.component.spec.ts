import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AvatarService } from '../avatar/avatar.service';
import { MAX_PICTURE_BYTES, PhotoEditorComponent, cropFor } from './photo-editor.component';

function fakeFileEvent(file: File): Event {
  return { target: { files: [file], value: 'x' } } as unknown as Event;
}

describe('cropFor', () => {
  it('should crop the full short side at zoom 1 with the pan centred', () => {
    expect(cropFor(400, 300, 1, 0.5, 0.5)).toEqual({ x: 50, y: 0, size: 300 });
  });

  it('should shrink the square as zoom increases and move it with the pan', () => {
    expect(cropFor(400, 300, 2, 0.5, 0.5)).toEqual({ x: 125, y: 75, size: 150 });
    expect(cropFor(400, 300, 2, 0, 1)).toEqual({ x: 0, y: 150, size: 150 });
  });

  it('should keep the crop inside the image when the pan is out of range', () => {
    expect(cropFor(400, 300, 1, 2, -1)).toEqual({ x: 100, y: 0, size: 300 });
  });
});

describe('PhotoEditorComponent', () => {
  let fixture: ComponentFixture<PhotoEditorComponent>;
  let component: PhotoEditorComponent;
  let avatarSpy: jasmine.SpyObj<AvatarService>;

  beforeEach(async () => {
    avatarSpy = jasmine.createSpyObj<AvatarService>('AvatarService', ['url', 'upload', 'remove']);
    avatarSpy.url.and.callFake((id: string, size: 96 | 256 = 96) => `/api/v1/avatars/${id}?size=${size}`);
    avatarSpy.upload.and.returnValue(of({ avatar_id: 'a2' }));
    avatarSpy.remove.and.returnValue(of(undefined));

    await TestBed.configureTestingModule({
      imports: [PhotoEditorComponent, getTranslocoTestingModule()],
      providers: [{ provide: AvatarService, useValue: avatarSpy }],
    }).compileComponents();

    fixture = TestBed.createComponent(PhotoEditorComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('memberId', 'm2');
    fixture.componentRef.setInput('name', 'Ava');
    fixture.componentRef.setInput('heading', 'My picture');
    fixture.componentRef.setInput('intro', 'Pick any photo.');
    fixture.componentRef.setInput('removeLabel', 'Use my letter instead');
    fixture.detectChanges();
  });

  it('should render the dialog heading and the letter-instead label', () => {
    const root = fixture.nativeElement as HTMLElement;
    expect(root.querySelector('#photo-dialog-title')?.textContent?.trim()).toBe('My picture');
    expect(root.querySelector('[data-testid="photo-remove"]')?.textContent).toContain('Use my letter instead');
  });

  it('should emit closed when Cancel is clicked', () => {
    const closed = jasmine.createSpy('closed');
    component.closed.subscribe(closed);
    (fixture.nativeElement.querySelector('[data-testid="photo-cancel"]') as HTMLButtonElement).click();
    expect(closed).toHaveBeenCalledTimes(1);
  });

  it('should emit closed when Escape is pressed', () => {
    const closed = jasmine.createSpy('closed');
    component.closed.subscribe(closed);
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(closed).toHaveBeenCalledTimes(1);
  });

  it('should reject a file that is not a JPG or PNG', () => {
    component.onFile(fakeFileEvent(new File(['GIF89a'], 'pic.gif', { type: 'image/gif' })));
    fixture.detectChanges();
    expect(component.errorKey()).toBe('avatar.error.type');
    expect(component.file()).toBeNull();
    expect(avatarSpy.upload).not.toHaveBeenCalled();
  });

  it('should reject a file over the 5 MB limit', () => {
    const big = new File([new Uint8Array(MAX_PICTURE_BYTES + 1)], 'big.jpg', { type: 'image/jpeg' });
    component.onFile(fakeFileEvent(big));
    expect(component.errorKey()).toBe('avatar.error.too_large');
    expect(component.file()).toBeNull();
  });

  it('should not allow saving before a picture is chosen', () => {
    expect(component.canSave()).toBeFalse();
    const save = fixture.nativeElement.querySelector('[data-testid="photo-save"]') as HTMLButtonElement;
    expect(save.disabled).toBeTrue();
  });

  it('should go back to the letter and emit null when the picture is removed', () => {
    fixture.componentRef.setInput('avatarId', 'a1');
    fixture.detectChanges();
    const emitted: (string | null)[] = [];
    const closed = jasmine.createSpy('closed');
    component.changed.subscribe((id) => emitted.push(id));
    component.closed.subscribe(closed);

    (fixture.nativeElement.querySelector('[data-testid="photo-remove"]') as HTMLButtonElement).click();

    expect(avatarSpy.remove).toHaveBeenCalledWith('m2');
    expect(emitted).toEqual([null]);
    expect(closed).toHaveBeenCalledTimes(1);
  });

  it('should show a network message when removing fails without a response', () => {
    avatarSpy.remove.and.returnValue(throwError(() => ({ code: 'network', status: 0 })));
    fixture.componentRef.setInput('avatarId', 'a1');
    fixture.detectChanges();

    component.removePicture();
    fixture.detectChanges();

    expect(component.errorKey()).toBe('avatar.error.network');
  });
});
