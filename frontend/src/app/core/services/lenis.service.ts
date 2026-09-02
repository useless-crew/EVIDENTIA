import { Injectable, Inject, PLATFORM_ID, NgZone, inject } from '@angular/core';
import { isPlatformBrowser } from '@angular/common';
import Lenis from 'lenis';

@Injectable({
  providedIn: 'root'
})
export class LenisService {
  private lenis: Lenis | null = null;
  private isBrowser: boolean;
  private rafId: number | null = null;
  private ngZone = inject(NgZone);

  constructor(@Inject(PLATFORM_ID) platformId: object) {
    this.isBrowser = isPlatformBrowser(platformId);
  }

  public init(): void {
    if (!this.isBrowser || this.lenis) {
      return;
    }

    this.ngZone.runOutsideAngular(() => {
      this.lenis = new Lenis({
        duration: 1.2,
        easing: (t: number) => Math.min(1, 1.001 - Math.pow(2, -10 * t)),
        orientation: 'vertical',
        gestureOrientation: 'vertical',
        smoothWheel: true,
        wheelMultiplier: 1,
        touchMultiplier: 1.5,
      });

      const raf = (time: number) => {
        this.lenis?.raf(time);
        this.rafId = requestAnimationFrame(raf);
      };

      this.rafId = requestAnimationFrame(raf);
    });
  }

  public scrollTo(
    target: string | number | HTMLElement,
    options: { offset?: number; duration?: number; immediate?: boolean } = {}
  ): void {
    if (!this.isBrowser) return;

    if (!this.lenis) {
      this.init();
    }

    if (!this.lenis) return;

    if (typeof target === 'number') {
      this.lenis.scrollTo(target, options);
      return;
    }

    const defaultOffset = -70; // Header height offset
    const offset = options.offset !== undefined ? options.offset : (typeof target === 'string' && target.startsWith('#') ? defaultOffset : 0);

    if (typeof target === 'string') {
      const element = document.querySelector(target) as HTMLElement;
      if (element) {
        this.lenis.scrollTo(element, { ...options, offset });
      }
    } else if (target) {
      this.lenis.scrollTo(target, { ...options, offset });
    }
  }

  public resize(): void {
    if (this.lenis) {
      this.lenis.resize();
    }
  }

  public stop(): void {
    if (this.lenis) {
      this.lenis.stop();
    }
  }

  public start(): void {
    if (this.lenis) {
      this.lenis.start();
    }
  }

  public getInstance(): Lenis | null {
    return this.lenis;
  }

  public destroy(): void {
    if (this.rafId !== null) {
      cancelAnimationFrame(this.rafId);
      this.rafId = null;
    }
    if (this.lenis) {
      this.lenis.destroy();
      this.lenis = null;
    }
  }
}

