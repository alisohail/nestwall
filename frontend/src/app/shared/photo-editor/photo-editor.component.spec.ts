import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AvatarService } from '../avatar/avatar.service';
import { PhotoEditorComponent, MAX_PICTURE_BYTES, UPLOAD_MAX_SIDE, cropFor } from './photo-editor.component';

function fakeFileEvent(file: File): Event {
  return { target: { files: [file], value: 'x' } } as unknown as Event;
}

/** A solid-colour picture encoded by the browser, so the test file decodes like a real photo. */
async function pictureFile(width: number, height: number, type: string): Promise<File> {
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const blob = await new Promise<Blob>((resolve) => canvas.toBlob((b) => resolve(b as Blob), type));
  return new File([blob], 'pic', { type });
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

  it('should convert a picture that is not a JPG or PNG to a JPEG before upload', async () => {
    const webp = await pictureFile(40, 30, 'image/webp');
    await component.onFile(fakeFileEvent(webp));
    fixture.detectChanges();
    expect(component.errorKey()).toBeNull();
    expect(component.file()?.type).toBe('image/jpeg');
    expect(component.image()?.naturalWidth).toBe(40);
  });

  it('should scale a large picture down so its long side is at most the upload limit', async () => {
    const large = await pictureFile(3000, 2000, 'image/png');
    await component.onFile(fakeFileEvent(large));
    fixture.detectChanges();
    expect(component.file()?.type).toBe('image/jpeg');
    expect(component.image()?.naturalWidth).toBe(UPLOAD_MAX_SIDE);
    expect(component.image()?.naturalHeight).toBe(Math.round(2000 * (UPLOAD_MAX_SIDE / 3000)));
  });

  it('should keep a small JPG or PNG as it is', async () => {
    const small = await pictureFile(40, 30, 'image/png');
    await component.onFile(fakeFileEvent(small));
    expect(component.file()).toBe(small);
  });

  it('should show a readable message when the browser cannot read the picture', async () => {
    await component.onFile(fakeFileEvent(new File(['not a picture'], 'pic.heic', { type: 'image/heic' })));
    expect(component.errorKey()).toBe('avatar.error.invalid_image');
    expect(component.file()).toBeNull();
    expect(avatarSpy.upload).not.toHaveBeenCalled();
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

  it('should show the size message for a 413 even when the body is not the app JSON', async () => {
    avatarSpy.upload.and.returnValue(throwError(() => ({ code: 'server_error', status: 413 })));
    await component.onFile(fakeFileEvent(await pictureFile(40, 30, 'image/png')));
    fixture.detectChanges();

    component.save();

    expect(avatarSpy.upload).toHaveBeenCalled();
    expect(component.errorKey()).toBe('avatar.error.too_large');
  });

  it('should refuse a picture that is still over 5 MB after conversion, without uploading it', async () => {
    const canvas = document.createElement('canvas');
    canvas.width = 2048;
    canvas.height = 2048;
    const ctx = canvas.getContext('2d') as CanvasRenderingContext2D;
    const pixels = ctx.createImageData(2048, 2048);
    for (let i = 0; i < pixels.data.length; i++) pixels.data[i] = Math.random() * 256;
    ctx.putImageData(pixels, 0, 0);
    const noise = await new Promise<Blob>((resolve) => canvas.toBlob((b) => resolve(b as Blob), 'image/png'));

    await component.onFile(fakeFileEvent(new File([noise], 'noise.png', { type: 'image/png' })));
    fixture.detectChanges();

    const kept = component.file();
    if (kept) {
      expect(kept.size).toBeLessThanOrEqual(MAX_PICTURE_BYTES);
    } else {
      expect(component.errorKey()).toBe('avatar.error.too_large');
    }
    expect(avatarSpy.upload).not.toHaveBeenCalled();
  });
});
