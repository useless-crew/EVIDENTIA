import {
  Directive,
  ElementRef,
  Input,
  OnDestroy,
  AfterViewInit,
  NgZone,
  inject
} from '@angular/core';
import gsap from 'gsap';
import { AnimationService } from '../services/animation.service';

/**
 * Magnetic hover — the element drifts toward the cursor and springs back on
 * exit. Applied to primary CTAs only; used everywhere it stops reading as a
 * deliberate accent and starts feeling unstable.
 *
 * Pointer listeners bind outside Angular so cursor movement never schedules
 * change detection.
 */
@Directive({
  selector: '[appMagnetic]',
  standalone: true
})
export class MagneticDirective implements AfterViewInit, OnDestroy {
  /** How far the element may travel, as a fraction of the cursor offset. */
  @Input() magneticStrength = 0.32;

  private host = inject(ElementRef<HTMLElement>);
  private ngZone = inject(NgZone);
  private animation = inject(AnimationService);

  private onMove = (event: PointerEvent) => {
    const el = this.host.nativeElement as HTMLElement;
    const rect = el.getBoundingClientRect();
    const dx = event.clientX - (rect.left + rect.width / 2);
    const dy = event.clientY - (rect.top + rect.height / 2);

    gsap.to(el, {
      x: dx * this.magneticStrength,
      y: dy * this.magneticStrength,
      duration: 0.6,
      ease: 'power3.out'
    });
  };

  private onLeave = () => {
    gsap.to(this.host.nativeElement, {
      x: 0,
      y: 0,
      duration: 0.7,
      ease: 'elastic.out(1, 0.4)'
    });
  };

  ngAfterViewInit(): void {
    if (typeof window === 'undefined' || this.animation.prefersReducedMotion) {
      return;
    }
    // Touch and coarse pointers have no hover, so the effect would only add jitter.
    if (!window.matchMedia('(hover: hover) and (pointer: fine)').matches) {
      return;
    }

    this.ngZone.runOutsideAngular(() => {
      const el = this.host.nativeElement as HTMLElement;
      el.addEventListener('pointermove', this.onMove);
      el.addEventListener('pointerleave', this.onLeave);
    });
  }

  ngOnDestroy(): void {
    const el = this.host.nativeElement as HTMLElement;
    el.removeEventListener('pointermove', this.onMove);
    el.removeEventListener('pointerleave', this.onLeave);
    gsap.killTweensOf(el);
  }
}
