// Decides when a press on a row turns into a drag-out. There is no HTML5
// drag here: once the pointer has travelled far enough with the primary
// button held, the caller hands the selection to the native drag (StartDrag).

export interface PointerLike {
  button: number;
  buttons: number;
  clientX: number;
  clientY: number;
}

export const DRAG_THRESHOLD = 5;

export class DragTracker<T> {
  private origin: { x: number; y: number; payload: T } | null = null;

  constructor(
    private readonly begin: (payload: T) => void,
    private readonly threshold = DRAG_THRESHOLD,
  ) {}

  /** A press is being tracked and has not become a drag yet. */
  get armed(): boolean {
    return this.origin !== null;
  }

  down(e: PointerLike, payload: T): void {
    this.origin = e.button === 0 ? { x: e.clientX, y: e.clientY, payload } : null;
  }

  /** Returns true when this move started the drag; it fires once per press. */
  move(e: PointerLike): boolean {
    const origin = this.origin;
    if (!origin) return false;
    if (e.buttons !== 1) {
      this.origin = null;
      return false;
    }
    if (Math.hypot(e.clientX - origin.x, e.clientY - origin.y) <= this.threshold) return false;
    // The native drag swallows the pointerup, so disarm before handing over.
    this.origin = null;
    this.begin(origin.payload);
    return true;
  }

  up(): void {
    this.origin = null;
  }
}

export type PressEvent = "pointerdown" | "mousedown" | "click";

/**
 * Makes up for the press a webview loses after a native drag.
 *
 * The drag takes the pointer away, so the webview never sees the button
 * come up and goes on believing it is held. WebKitGTK then withholds
 * `pointerdown` for the next press: on X11 that press still arrives as
 * `mousedown`, on Wayland as nothing but the `click` that ends it. Rows are
 * picked and armed on `pointerdown`, so without this the first press after
 * every drag-out would do nothing.
 *
 * `role` says what each event is to the list. Outside the aftermath of a
 * drag only `pointerdown` counts, so a press is never handled twice.
 */
export class LostRelease {
  private pending = false;

  /** A native drag has taken the pointer; the release will not reach the webview. */
  dragStarted(): void {
    this.pending = true;
  }

  /** The press that was at risk has been delivered, here or elsewhere in the window. */
  settle(): void {
    this.pending = false;
  }

  /**
   * "press": treat the event as the start of a press. "click": the whole
   * press was lost and this is its end, a press and release in one. null:
   * nothing to do, the press was or will be handled by another event.
   */
  role(type: PressEvent): "press" | "click" | null {
    if (type === "pointerdown") {
      this.pending = false;
      return "press";
    }
    if (!this.pending) return null;
    this.pending = false;
    return type === "mousedown" ? "press" : "click";
  }
}
