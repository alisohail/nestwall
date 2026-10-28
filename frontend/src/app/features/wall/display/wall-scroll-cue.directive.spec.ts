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
class HostComponent {}

describe("WallScrollCueDirective", () => {
  let fixture: ComponentFixture<HostComponent>;
  let list: HTMLElement;

  beforeEach(() => {
    TestBed.configureTestingModule({ imports: [HostComponent] });
    fixture = TestBed.createComponent(HostComponent);
    fixture.detectChanges();
    list = fixture.nativeElement.querySelector(".list");
  });

  it("marks a list that has content hidden below", () => {
    expect(list.hasAttribute("data-more")).toBeTrue();
  });

  it("clears the mark once the list is scrolled to the end", () => {
    list.scrollTop = list.scrollHeight;
    list.dispatchEvent(new Event("scroll"));
    expect(list.hasAttribute("data-more")).toBeFalse();
  });

  it("does not mark a list that fits", () => {
    list.style.height = "1000px";
    list.dispatchEvent(new Event("scroll"));
    expect(list.hasAttribute("data-more")).toBeFalse();
  });

  describe("horizontal list", () => {
    let row: HTMLElement;

    beforeEach(() => {
      row = fixture.nativeElement.querySelector(".row");
    });

    it("marks a row that has content hidden to the right", () => {
      expect(row.hasAttribute("data-more")).toBeTrue();
    });

    it("clears the mark once the row is scrolled to the end", () => {
      row.scrollLeft = row.scrollWidth;
      row.dispatchEvent(new Event("scroll"));
      expect(row.hasAttribute("data-more")).toBeFalse();
    });

    it("does not mark a row that fits", () => {
      row.style.width = "1000px";
      row.dispatchEvent(new Event("scroll"));
      expect(row.hasAttribute("data-more")).toBeFalse();
    });
  });
});
