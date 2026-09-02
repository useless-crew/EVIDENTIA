import { Injectable, NgZone, inject } from '@angular/core';
import Lenis from 'lenis';
import gsap from 'gsap';

@Injectable({
  providedIn: 'root'
})
export class SmoothScrollService {
  private lenis: Lenis | null = null;
  private ngZone = inject(NgZone);

  init(wrapper?: HTMLElement) {
    if (typeof window === 'undefined') return;

    this.ngZone.runOutsideAngular(() => {
      this.lenis = new Lenis({
        duration: 1.1,
        easing: (t: number) => Math.min(1, 1.001 - Math.pow(2, -10 * t)),
        smoothWheel: true,
        ...(wrapper ? { wrapper } : {})
      });

      // Connect Lenis to GSAP ticker
      gsap.ticker.add((time: number) => {
        this.lenis?.raf(time * 1000);
      });

      gsap.ticker.lagSmoothing(0);
    });
  }

  scrollTo(target: string | number | HTMLElement, options?: any) {
    this.lenis?.scrollTo(target, options);
  }

  destroy() {
    this.lenis?.destroy();
    this.lenis = null;
  }
}
