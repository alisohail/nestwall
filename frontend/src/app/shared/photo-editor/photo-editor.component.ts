import { Component, DestroyRef, ElementRef, HostListener, computed, effect, inject, input, output, signal, viewChild } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { ApiError } from '../../core/api/api-client.service';
import { AvatarComponent } from '../avatar/avatar.component';
import { AvatarService, CropRect } from '../avatar/avatar.service';
import { IconComponent } from '../ui/icon/icon.component';

export const MAX_PICTURE_BYTES = 5 * 1024 * 1024;
export const PREVIEW_PX = 240;
const ACCEPTED_TYPES = ['image/jpeg', 'image/png'];

/**
 * Square crop from a source image. Zoom 1 uses the whole short side. Pan values run from 0 to 1 across
 * the room left over after cropping, so the crop always stays inside the image.
 */
export function cropFor(width: number, height: number, zoom: number, panX: number, panY: number): CropRect {
  const size = Math.max(1, Math.round(Math.min(width, height) / zoom));
  const x = Math.round(clamp01(panX) * (width - size));
  const y = Math.round(clamp01(panY) * (height - size));
  return { x, y, size };
}

function clamp01(value: number): number {
  return Math.min(1, Math.max(0, value));
}

function uploadErrorKey(err: ApiError): string {
  if (err.status === 0) return 'avatar.error.network';
  switch (err.code) {
    case 'image_too_large':
      return 'avatar.error.too_large';
    case 'unsupported_type':
      return 'avatar.error.type';
    case 'animated_image':
      return 'avatar.error.animated';
    case 'image_dimensions':
      return 'avatar.error.dimensions';
    case 'invalid_image':
    case 'invalid_crop':
      return 'avatar.error.invalid_image';
    case 'avatar_changes_locked':
      return 'avatar.error.locked';
    case 'forbidden':
      return 'avatar.error.forbidden';
    default:
      return 'avatar.error.generic';
  }
}

/**
 * "My picture" dialog. The member picks a JPG or PNG (from files or the camera), crops it to a square,
 * then saves it, or goes back to the letter. Hosts open it with @if and close it on (closed).
 */
@Component({
  selector: 'app-photo-editor',
  standalone: true,
  imports: [TranslocoModule, AvatarComponent, IconComponent],
  templateUrl: './photo-editor.component.html',
  styleUrl: './photo-editor.component.scss',
})
export class PhotoEditorComponent {
  private avatars = inject(AvatarService);
  private destroyRef = inject(DestroyRef);

  memberId = input.required<string>();
  name = input.required<string>();
  colour = input<string>('#888888');
  avatarId = input<string | null>(null);
  /** Translated title and intro line, and the label of the button that goes back to the letter. */
  heading = input.required<string>();
  intro = input.required<string>();
  removeLabel = input.required<string>();

  /** Emits the new avatar ID after a save, or null after the member goes back to the letter. */
  changed = output<string | null>();
  closed = output<void>();

  readonly accept = ACCEPTED_TYPES.join(',');
  readonly maxBytes = MAX_PICTURE_BYTES;

  canvas = viewChild<ElementRef<HTMLCanvasElement>>('preview');
  fileInput = viewChild<ElementRef<HTMLInputElement>>('fileInput');
  cameraInput = viewChild<ElementRef<HTMLInputElement>>('cameraInput');

  file = signal<File | null>(null);
  image = signal<HTMLImageElement | null>(null);
  zoom = signal(1);
  panX = signal(0.5);
  panY = signal(0.5);
  saving = signal(false);
  errorKey = signal<string | null>(null);

  crop = computed<CropRect | null>(() => {
    const img = this.image();
    if (!img) return null;
    return cropFor(img.naturalWidth, img.naturalHeight, this.zoom(), this.panX(), this.panY());
  });

  canSave = computed(() => this.file() !== null && this.crop() !== null && !this.saving());

  private dragStart: { x: number; y: number; panX: number; panY: number } | null = null;

  constructor() {
    effect(() => {
      const crop = this.crop();
      const el = this.canvas()?.nativeElement;
      const img = this.image();
      if (!crop || !el || !img) return;
      const ctx = el.getContext('2d');
      if (!ctx) return;
      el.width = PREVIEW_PX;
      el.height = PREVIEW_PX;
      ctx.drawImage(img, crop.x, crop.y, crop.size, crop.size, 0, 0, PREVIEW_PX, PREVIEW_PX);
    });
  }

  chooseFile(): void {
    this.fileInput()?.nativeElement.click();
  }

  chooseCamera(): void {
    this.cameraInput()?.nativeElement.click();
  }

  cancel(): void {
    if (!this.saving()) this.closed.emit();
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.cancel();
  }

  onFile(event: Event): void {
    const input = event.target as HTMLInputElement;
    const selected = input.files?.[0] ?? null;
    input.value = '';
    this.errorKey.set(null);
    if (!selected) return;
    if (!ACCEPTED_TYPES.includes(selected.type)) {
      this.errorKey.set('avatar.error.type');
      return;
    }
    if (selected.size > MAX_PICTURE_BYTES) {
      this.errorKey.set('avatar.error.too_large');
      return;
    }
    this.loadImage(selected);
  }

  setZoom(event: Event): void {
    this.zoom.set(Number((event.target as HTMLInputElement).value));
  }

  setPanX(event: Event): void {
    this.panX.set(clamp01(Number((event.target as HTMLInputElement).value)));
  }

  setPanY(event: Event): void {
    this.panY.set(clamp01(Number((event.target as HTMLInputElement).value)));
  }

  onPointerDown(event: PointerEvent): void {
    if (!this.image()) return;
    (event.target as HTMLElement).setPointerCapture?.(event.pointerId);
    this.dragStart = { x: event.clientX, y: event.clientY, panX: this.panX(), panY: this.panY() };
  }

  onPointerMove(event: PointerEvent): void {
    const start = this.dragStart;
    const img = this.image();
    const crop = this.crop();
    const el = this.canvas()?.nativeElement;
    if (!start || !img || !crop || !el) return;
    const shown = el.getBoundingClientRect().width || PREVIEW_PX;
    // Convert the pointer distance on screen into source pixels, then into the pan range.
    const sourcePerScreenPx = crop.size / shown;
    const slackX = img.naturalWidth - crop.size;
    const slackY = img.naturalHeight - crop.size;
    if (slackX > 0) {
      this.panX.set(clamp01(start.panX - ((event.clientX - start.x) * sourcePerScreenPx) / slackX));
    }
    if (slackY > 0) {
      this.panY.set(clamp01(start.panY - ((event.clientY - start.y) * sourcePerScreenPx) / slackY));
    }
  }

  onPointerUp(): void {
    this.dragStart = null;
  }

  save(): void {
    const file = this.file();
    const crop = this.crop();
    if (!file || !crop || this.saving()) return;
    this.saving.set(true);
    this.errorKey.set(null);
    this.avatars
      .upload(this.memberId(), file, crop)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.saving.set(false);
          this.changed.emit(res.avatar_id);
          this.closed.emit();
        },
        error: (err: ApiError) => {
          this.saving.set(false);
          this.errorKey.set(uploadErrorKey(err));
        },
      });
  }

  removePicture(): void {
    if (this.saving()) return;
    this.saving.set(true);
    this.errorKey.set(null);
    this.avatars
      .remove(this.memberId())
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.saving.set(false);
          this.changed.emit(null);
          this.closed.emit();
        },
        error: (err: ApiError) => {
          this.saving.set(false);
          this.errorKey.set(uploadErrorKey(err));
        },
      });
  }

  private loadImage(file: File): void {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      URL.revokeObjectURL(url);
      this.file.set(file);
      this.zoom.set(1);
      this.panX.set(0.5);
      this.panY.set(0.5);
      this.image.set(img);
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      this.errorKey.set('avatar.error.invalid_image');
    };
    img.src = url;
  }
}
