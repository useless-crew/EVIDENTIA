import {
  Component,
  ElementRef,
  ViewChild,
  AfterViewInit,
  OnDestroy,
  NgZone,
  inject
} from '@angular/core';
import gsap from 'gsap';
import { AnimationService } from '../../core/services/animation.service';

/**
 * Custom cursor: a small filled dot that tracks the pointer exactly, trailed by
 * a ring that lags behind it. Over anything interactive the ring expands and
 * the dot shrinks.
 *
 * The native cursor stays visible on touch devices, coarse pointers, and for
 * reduced-motion users — in those cases this component renders nothing.
 */
@Component({
  selector: 'app-cursor',
  standalone: true,
  template: `
    <div #ring class="cursor-ring" aria-hidden="true"></div>
    <div #dot class="cursor-dot" aria-hidden="true"></div>
  `,
  styles: [
    `
      :host {
        position: fixed;
        inset: 0;
        z-index: 9999;
        pointer-events: none;
        display: none;
      }

      :host(.cursor-active) {
        display: block;
      }

      .cursor-dot,
      .cursor-ring {
        position: fixed;
        top: 0;
        left: 0;
        border-radius: 50%;
        pointer-events: none;
        will-change: transform;
      }

      .cursor-dot {
        width: 6px;
        height: 6px;
        margin: -3px 0 0 -3px;
        background: var(--color-navy-primary);
      }

      .cursor-ring {
        width: 34px;
        height: 34px;
        margin: -17px 0 0 -17px;
        border: 1px solid rgba(19, 42, 73, 0.42);
        transition:
          width 0.32s var(--ease-out-expo),
          height 0.32s var(--ease-out-expo),
          margin 0.32s var(--ease-out-expo),
          border-color 0.32s var(--ease-out-expo),
          background-color 0.32s var(--ease-out-expo);
      }

      :host(.cursor-hover) .cursor-ring {
        width: 60px;
        height: 60px;
        margin: -30px 0 0 -30px;
        border-color: var(--color-accent-blue);
        background: rgba(66, 109, 155, 0.1);
      }

      :host(.cursor-hover) .cursor-dot {
        opacity: 0;
      }

      /* Over dark sections the cursor inverts so it stays visible */
      :host(.cursor-inverted) .cursor-dot {
        background: var(--tricolor-saffron);
      }

      :host(.cursor-inverted) .cursor-ring {
        border-color: rgba(255, 255, 255, 0.5);
      }
    `
  ]
})
export class CursorComponent implements AfterViewInit, OnDestroy {
  @ViewChild('dot') dotRef!: ElementRef<HTMLElement>;
  @ViewChild('ring') ringRef!: ElementRef<HTMLElement>;

  private hostRef = inject(ElementRef<HTMLElement>);
  private ngZone = inject(NgZone);
  private animation = inject(AnimationService);
  private enabled = false;

  private readonly interactiveSelector =
    'a, button, [role="button"], input, select, textarea, summary, [data-cursor="hover"]';

  private onMove = (event: PointerEvent) => {
    gsap.to(this.dotRef.nativeElement, {
      x: event.clientX,
      y: event.clientY,
      duration: 0.08,
      ease: 'none'
    });

    gsap.to(this.ringRef.nativeElement, {
      x: event.clientX,
      y: event.clientY,
      duration: 0.5,
      ease: 'power3.out'
    });
  };

  private onOver = (event: Event) => {
    const target = event.target as HTMLElement | null;
    if (!target?.closest) return;

    const host = this.hostRef.nativeElement as HTMLElement;
    host.classList.toggle('cursor-hover', !!target.closest(this.interactiveSelector));
    host.classList.toggle('cursor-inverted', !!target.closest('.on-dark'));
  };

  ngAfterViewInit(): void {
    if (typeof window === 'undefined' || this.animation.prefersReducedMotion) {
      return;
    }
    if (!window.matchMedia('(hover: hover) and (pointer: fine)').matches) {
      return;
    }

    this.enabled = true;
    const host = this.hostRef.nativeElement as HTMLElement;
    host.classList.add('cursor-active');
    document.documentElement.classList.add('has-custom-cursor');

    this.ngZone.runOutsideAngular(() => {
      window.addEventListener('pointermove', this.onMove, { passive: true });
      document.addEventListener('pointerover', this.onOver, { passive: true });
    });
  }

  ngOnDestroy(): void {
    if (!this.enabled) return;
    window.removeEventListener('pointermove', this.onMove);
    document.removeEventListener('pointerover', this.onOver);
    document.documentElement.classList.remove('has-custom-cursor');
  }
}
