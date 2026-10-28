import {
  AfterViewInit,
  Directive,
  ElementRef,
  OnDestroy,
  inject,
  input,
} from "@angular/core";

/**
 * Sets data-more on a scroll container while content is hidden past its edge,
 * so the template's .scroll-cue shows there. The default axis is vertical
 * (bottom edge); pass "x" for a horizontal list (right edge).
 */
@Directive({
  selector: "[appWallScrollCue]",
  standalone: true,
  host: { "(scroll)": "update()" },
})
export class WallScrollCueDirective implements AfterViewInit, OnDestroy {
  readonly axis = input("", { alias: "appWallScrollCue" });
  private readonly host = inject<ElementRef<HTMLElement>>(ElementRef);
  private resize?: ResizeObserver;
  private mutation?: MutationObserver;

  ngAfterViewInit(): void {
    const el = this.host.nativeElement;
    this.resize = new ResizeObserver(() => this.update());
    this.resize.observe(el);
    this.mutation = new MutationObserver(() => this.update());
    this.mutation.observe(el, { childList: true, subtree: true });
    this.update();
  }

  ngOnDestroy(): void {
    this.resize?.disconnect();
    this.mutation?.disconnect();
  }

  update(): void {
    const el = this.host.nativeElement;
    if (this.axis() === "x") {
      el.toggleAttribute("data-more", el.scrollLeft + el.clientWidth < el.scrollWidth - 4);
      el.toggleAttribute("data-more-start", el.scrollLeft > 4);
      return;
    }
    el.toggleAttribute("data-more", el.scrollTop + el.clientHeight < el.scrollHeight - 4);
  }
}
