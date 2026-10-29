import {
  AfterViewInit,
  Component,
  ElementRef,
  computed,
  inject,
  signal,
  viewChildren,
  ChangeDetectionStrategy
} from "@angular/core";
import { Router, RouterLink } from "@angular/router";
import { TranslocoModule } from "@ngneat/transloco";
import { ApiError } from "../../../core/api/api-client.service";
import { BannerComponent } from "../../../shared/ui/banner/banner.component";
import { LoadingComponent } from "../../../shared/ui/loading/loading.component";
import { WallDevice, WallPairService } from "./wall-pair.service";

const KEY_LENGTH = 8;
/** Same alphabet as the server (no I, L, O, 0 or 1), so a key read off the parent's screen is never ambiguous. */
const LETTERS = "ABCDEFGHJKMNPQRSTUVWXYZ".split("");
const DIGITS = "23456789".split("");
const KEY_CHARS = new Set([...LETTERS, ...DIGITS]);

/** Returns the key character for typed text, or null when it is outside the alphabet. */
function toKeyChar(raw: string): string | null {
  const ch = raw.toUpperCase();
  return KEY_CHARS.has(ch) ? ch : null;
}

@Component({
    selector: "app-wall-pair",
    imports: [RouterLink, TranslocoModule, BannerComponent, LoadingComponent],
    templateUrl: "./wall-pair.component.html",
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: "./wall-pair.component.scss"
})
export class WallPairComponent implements AfterViewInit {
  private pairService = inject(WallPairService);
  private router = inject(Router);
  private slotInputs = viewChildren<ElementRef<HTMLInputElement>>("slot");

  readonly slots = Array.from({ length: KEY_LENGTH }, (_, i) => i);

  readonly chars = signal<string[]>(Array<string>(KEY_LENGTH).fill(""));
  readonly loading = signal(false);
  readonly error = signal<string | null>(null);
  readonly paired = signal<WallDevice | null>(null);
  readonly complete = computed(() => this.chars().every((ch) => ch !== ""));

  ngAfterViewInit(): void {
    this.focusSlot(0);
  }

  onInput(index: number, event: Event): void {
    const input = event.target as HTMLInputElement;
    // The newest valid character wins, so typing over a filled box replaces it.
    const typed = [...input.value]
      .map(toKeyChar)
      .filter((ch): ch is string => ch !== null);
    if (input.value === "") {
      this.setSlot(index, "");
      return;
    }
    const ch = typed.at(-1) ?? null;
    if (ch === null) {
      input.value = this.chars()[index];
      return;
    }
    input.value = ch;
    this.error.set(null);
    this.setSlot(index, ch);
    this.focusSlot(index + 1);
  }

  onKeydown(index: number, event: KeyboardEvent): void {
    if (event.key === "Enter") {
      event.preventDefault();
      this.submit();
    } else if (
      event.key === "Backspace" &&
      this.chars()[index] === "" &&
      index > 0
    ) {
      event.preventDefault();
      this.setSlot(index - 1, "");
      this.focusSlot(index - 1);
    } else if (event.key === "ArrowLeft") {
      event.preventDefault();
      this.focusSlot(index - 1);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      this.focusSlot(index + 1);
    }
  }

  onPaste(index: number, event: ClipboardEvent): void {
    event.preventDefault();
    const text = event.clipboardData?.getData("text") ?? "";
    const keys = [...text]
      .map(toKeyChar)
      .filter((ch): ch is string => ch !== null);
    if (keys.length === 0 || this.loading() || this.paired()) return;

    this.error.set(null);
    const next = [...this.chars()];
    keys
      .slice(0, KEY_LENGTH - index)
      .forEach((ch, offset) => (next[index + offset] = ch));
    this.chars.set(next);
    this.focusSlot(Math.min(index + keys.length, KEY_LENGTH - 1));
  }

  submit(): void {
    if (!this.complete() || this.loading() || this.paired()) return;

    this.loading.set(true);
    this.error.set(null);
    this.pairService.pair(this.chars().join("")).subscribe({
      next: (device) => {
        this.loading.set(false);
        this.paired.set(device);
        this.router.navigateByUrl("/wall");
      },
      error: (err: ApiError) => {
        this.loading.set(false);
        // A rejected key is expired, used or mistyped, so it is cleared. Other errors keep it for a retry.
        if (err.code === "pairing_key_invalid") {
          this.chars.set(Array<string>(KEY_LENGTH).fill(""));
          this.focusSlot(0);
        }
        this.error.set(this.errorKey(err));
      },
    });
  }

  private setSlot(index: number, ch: string): void {
    this.chars.update((current) => {
      const next = [...current];
      next[index] = ch;
      return next;
    });
  }

  private focusSlot(index: number): void {
    const input = this.slotInputs()[index]?.nativeElement;
    if (!input) return;
    input.focus();
    input.select();
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return "wall.pair.error.network";
    switch (err.code) {
      case "pairing_key_invalid":
        return "wall.pair.error.invalid_key";
      case "rate_limited":
        return "wall.pair.error.rate_limited";
      default:
        return "wall.pair.error.generic";
    }
  }
}
