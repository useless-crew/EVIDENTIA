import { Injectable, NgZone, inject } from '@angular/core';
import gsap from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';
import { LenisService } from './lenis.service';

/**
 * Central GSAP surface for the app.
 *
 * ScrollTrigger is registered exactly once here and driven off Lenis's own
 * scroll callback — without that hand-off, ScrollTrigger reads the native
 * scroll position while Lenis is interpolating a different one, and every
 * scroll-linked animation fires a frame or two out of sync.
 */
@Injectable({
  providedIn: 'root'
})
export class AnimationService {
  private ngZone = inject(NgZone);
  private lenisService = inject(LenisService);
  private scrollTriggerReady = false;

  /** Idempotent — safe to call from every component that needs scroll animation. */
  initScrollTrigger(): void {
    if (this.scrollTriggerReady || typeof window === 'undefined') {
      return;
    }
    this.scrollTriggerReady = true;

    this.ngZone.runOutsideAngular(() => {
      gsap.registerPlugin(ScrollTrigger);

      // Lenis scrolls the window natively (no transform wrapper), so
      // ScrollTrigger's default scroller is already correct and needs no
      // scrollerProxy. It only needs to be told to update on Lenis's own
      // interpolated frames rather than waiting for native scroll events.
      this.lenisService.getInstance()?.on('scroll', ScrollTrigger.update);

      ScrollTrigger.refresh();
    });
  }

  get prefersReducedMotion(): boolean {
    return (
      typeof window !== 'undefined' &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches
    );
  }

  /** Recalculate trigger positions after layout changes (accordion open, route swap). */
  refresh(): void {
    if (this.scrollTriggerReady) {
      ScrollTrigger.refresh();
    }
  }

  animateScreenEnter(element: HTMLElement, delay: number = 0) {
    this.ngZone.runOutsideAngular(() => {
      gsap.fromTo(
        element,
        { opacity: 0, y: 12 },
        { opacity: 1, y: 0, duration: 0.35, ease: 'power2.out', delay }
      );
    });
  }

  animateModalEnter(element: HTMLElement) {
    this.ngZone.runOutsideAngular(() => {
      gsap.fromTo(
        element,
        { opacity: 0, scale: 0.95, y: -10 },
        { opacity: 1, scale: 1, y: 0, duration: 0.28, ease: 'back.out(1.4)' }
      );
    });
  }

  animateNodePulse(element: HTMLElement) {
    this.ngZone.runOutsideAngular(() => {
      gsap.fromTo(
        element,
        { scale: 1 },
        { scale: 1.18, duration: 0.2, yoyo: true, repeat: 1, ease: 'power1.inOut' }
      );
    });
  }
}
