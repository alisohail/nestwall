import { Component, ChangeDetectionStrategy } from "@angular/core";
import { ComponentFixture, TestBed } from "@angular/core/testing";
import { WallScrollCueDirective } from "./wall-scroll-cue.directive";

@Component({
    imports: [WallScrollCueDirective],
    changeDetection: ChangeDetectionStrategy.Eager,
    template: `
    <div class="list" appWallScrollCue style="height: 100px; overflow-y: auto">
      <div style="height: 400px"></div>
    </div>
    <div class="row" appWallScrollCue="x" style="width: 100px; overflow-x: auto">
      <div style="width: 400px; height: 20px"></div>
    </div>
  `
})
class HostComponent {
}

// jsdom has no layout, so give the element a fixed content size and read its visible size from the inline style.
function fakeScrollMetrics(el: HTMLElement, axis: "x" | "y", content: number): void {
    const size = axis === "y" ? "Height" : "Width";
    const style = axis === "y" ? "height" : "width";
    let offset = 0;
    Object.defineProperties(el, {
        [`scroll${size}`]: { get: () => content },
        [`client${size}`]: { get: () => parseFloat(el.style[style]) || 0 },
        [`scroll${axis === "y" ? "Top" : "Left"}`]: {
            get: () => offset,
            set: (value: number) => { offset = value; },
        },
    });
}

describe("WallScrollCueDirective", () => {
    let fixture: ComponentFixture<HostComponent>;
    let list: HTMLElement;

    beforeEach(() => {
        TestBed.configureTestingModule({ imports: [HostComponent] });
        fixture = TestBed.createComponent(HostComponent);
        list = fixture.nativeElement.querySelector(".list");
        fakeScrollMetrics(list, "y", 400);
        fakeScrollMetrics(fixture.nativeElement.querySelector(".row"), "x", 400);
        fixture.detectChanges();
    });

    it("marks a list that has content hidden below", () => {
        expect(list.hasAttribute("data-more")).toBe(true);
    });

    it("clears the mark once the list is scrolled to the end", () => {
        list.scrollTop = list.scrollHeight;
        list.dispatchEvent(new Event("scroll"));
        expect(list.hasAttribute("data-more")).toBe(false);
    });

    it("does not mark a list that fits", () => {
        list.style.height = "1000px";
        list.dispatchEvent(new Event("scroll"));
        expect(list.hasAttribute("data-more")).toBe(false);
    });

    describe("horizontal list", () => {
        let row: HTMLElement;

        beforeEach(() => {
        row = fixture.nativeElement.querySelector(".row");
        });

        it("marks a row that has content hidden to the right", () => {
            expect(row.hasAttribute("data-more")).toBe(true);
        });

        it("clears the mark once the row is scrolled to the end", () => {
            row.scrollLeft = row.scrollWidth;
            row.dispatchEvent(new Event("scroll"));
            expect(row.hasAttribute("data-more")).toBe(false);
        });

        it("does not mark a row that fits", () => {
            row.style.width = "1000px";
            row.dispatchEvent(new Event("scroll"));
            expect(row.hasAttribute("data-more")).toBe(false);
        });
    });
});
