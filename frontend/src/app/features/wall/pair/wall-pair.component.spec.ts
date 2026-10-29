import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from "@angular/core/testing";
import { Router, provideRouter } from "@angular/router";
import { of, throwError } from "rxjs";
import { getTranslocoTestingModule } from "../../../core/transloco/transloco-testing.module";
import { ApiError } from "../../../core/api/api-client.service";
import { WallPairComponent } from "./wall-pair.component";
import { WallDevice, WallPairService } from "./wall-pair.service";
import { mockOf } from "../../../testing/mock-of";

const device: WallDevice = {
    id: "d1",
    name: "Kitchen wall",
    paired_at: "2025-01-01T10:00:00Z",
};

describe("WallPairComponent", () => {
    let fixture: ComponentFixture<WallPairComponent>;
    let component: WallPairComponent;
    let pairSpy: MockedObject<WallPairService>;

    function slot(index: number): HTMLInputElement {
        return fixture.nativeElement.querySelector(`[data-testid="pair-slot-${index}"]`);
    }

    function submitButton(): HTMLButtonElement {
        return fixture.nativeElement.querySelector('[data-testid="pair-submit"]');
    }

    /** Simulates the browser delivering typed text to a box. */
    function typeInSlot(index: number, text: string): void {
        const input = slot(index);
        input.value = text;
        input.dispatchEvent(new Event("input", { bubbles: true }));
        fixture.detectChanges();
    }

    function pressKey(index: number, key: string): void {
        slot(index).dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
        fixture.detectChanges();
    }

    function paste(index: number, text: string): void {
        const clipboardData = new DataTransfer();
        clipboardData.setData("text", text);
        slot(index).dispatchEvent(new ClipboardEvent("paste", {
            clipboardData,
            bubbles: true,
            cancelable: true,
        }));
        fixture.detectChanges();
    }

    function typeKey(text: string): void {
        [...text].forEach((ch, i) => typeInSlot(i, ch));
    }

    beforeEach(async () => {
        pairSpy = mockOf<WallPairService>({
            pair: vi.fn().mockName("WallPairService.pair")
        });

        await TestBed.configureTestingModule({
            imports: [WallPairComponent, getTranslocoTestingModule()],
            providers: [
                { provide: WallPairService, useValue: pairSpy },
                provideRouter([]),
            ],
        }).compileComponents();

        fixture = TestBed.createComponent(WallPairComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    });

    it("should create", () => {
        expect(component).toBeTruthy();
    });

    it("should render eight input boxes and no keypad or expiry note", () => {
        const inputs = fixture.nativeElement.querySelectorAll('[data-testid="pair-slots"] input');
        expect(inputs.length).toBe(8);
        expect(fixture.nativeElement.querySelector(".pair-keys")).toBeNull();
        expect(fixture.nativeElement.querySelector(".pair-note")).toBeNull();
    });

    it("should focus the first box on start", () => {
        expect(document.activeElement).toBe(slot(0));
    });

    it("should link back to the sign-in page", () => {
        const back: HTMLAnchorElement = fixture.nativeElement.querySelector('[data-testid="pair-back"]');
        expect(back.getAttribute("href")).toBe("/login");
    });

    it("should keep pairing disabled until eight characters are entered", () => {
        typeKey("ABCD234");
        expect(submitButton().disabled).toBe(true);

        typeKey("ABCD2345");
        expect(component.complete()).toBe(true);
        expect(submitButton().disabled).toBe(false);
    });

    it("should advance focus as characters are typed", () => {
        typeInSlot(0, "a");
        expect(component.chars()[0]).toBe("A");
        expect(document.activeElement).toBe(slot(1));
    });

    it("should replace a filled box with the newest valid character", () => {
        typeInSlot(0, "A");
        slot(0).focus();
        typeInSlot(0, "AB");
        expect(component.chars()[0]).toBe("B");
        expect(slot(0).value).toBe("B");
    });

    it("should reject characters outside the key alphabet", () => {
        typeInSlot(0, "I");
        typeInSlot(0, "0");
        expect(component.chars()[0]).toBe("");
        expect(slot(0).value).toBe("");
    });

    it("should move back and clear the previous box on backspace in an empty box", () => {
        typeKey("AB");
        pressKey(2, "Backspace");
        expect(component.chars()).toEqual(["A", "", "", "", "", "", "", ""]);
        expect(document.activeElement).toBe(slot(1));
    });

    it("should clear a filled box when its text is deleted", () => {
        typeKey("AB");
        typeInSlot(1, "");
        expect(component.chars()[1]).toBe("");
    });

    it("should move focus with the arrow keys", () => {
        typeKey("AB");
        pressKey(2, "ArrowLeft");
        expect(document.activeElement).toBe(slot(1));
        pressKey(1, "ArrowRight");
        expect(document.activeElement).toBe(slot(2));
    });

    it("should fill the boxes from a pasted key and drop invalid characters", () => {
        paste(0, "abcd-2345");
        expect(component.chars()).toEqual(["A", "B", "C", "D", "2", "3", "4", "5"]);
        expect(component.complete()).toBe(true);
    });

    it("should fill from the box that received the paste", () => {
        paste(3, "XY");
        expect(component.chars()).toEqual(["", "", "", "X", "Y", "", "", ""]);
    });

    it("should submit on Enter when the key is complete", () => {
        pairSpy.pair.mockReturnValue(of(device));
        vi.spyOn(TestBed.inject(Router), "navigateByUrl").mockResolvedValue(true);
        typeKey("ABCD2345");
        pressKey(7, "Enter");

        expect(pairSpy.pair).toHaveBeenCalledWith("ABCD2345");
    });

    it("should not submit on Enter while the key is incomplete", () => {
        typeKey("ABCD");
        pressKey(4, "Enter");
        expect(pairSpy.pair).not.toHaveBeenCalled();
    });

    it("should send the typed key and go to the wall once paired", () => {
        const router = TestBed.inject(Router);
        vi.spyOn(router, "navigateByUrl").mockResolvedValue(true);
        pairSpy.pair.mockReturnValue(of(device));
        typeKey("ABCD2345");
        component.submit();
        fixture.detectChanges();

        expect(pairSpy.pair).toHaveBeenCalledWith("ABCD2345");
        expect(component.paired()).toEqual(device);
        expect(router.navigateByUrl).toHaveBeenCalledWith("/wall");
    });

    it("should clear the key and return to the first box when the server rejects it", () => {
        const err: ApiError = { code: "pairing_key_invalid", status: 400 };
        pairSpy.pair.mockReturnValue(throwError(() => err));
        typeKey("ABCD2345");
        component.submit();
        fixture.detectChanges();

        expect(component.error()).toBe("wall.pair.error.invalid_key");
        expect(component.chars()).toEqual(["", "", "", "", "", "", "", ""]);
        expect(document.activeElement).toBe(slot(0));
        expect(fixture.nativeElement.querySelector('[data-testid="pair-error"]')).toBeTruthy();
    });

    it("should keep the key when the server rate-limits the attempt", () => {
        const err: ApiError = { code: "rate_limited", status: 429 };
        pairSpy.pair.mockReturnValue(throwError(() => err));
        typeKey("ABCD2345");
        component.submit();

        expect(component.error()).toBe("wall.pair.error.rate_limited");
        expect(component.complete()).toBe(true);
    });

    it("should not call the server before the key is complete", () => {
        typeKey("ABCD");
        component.submit();
        expect(pairSpy.pair).not.toHaveBeenCalled();
    });
});
