import { Injectable, Inject, PLATFORM_ID } from '@angular/core';
import { isPlatformBrowser } from '@angular/common';
import Lenis from 'lenis';

@Injectable({
  providedIn: 'root'
})
export class LenisService {
  private lenis: Lenis | null = null;
  private isBrowser: boolean;
  private rafId: number | null = null;

  constructor(@Inject(PLATFORM_ID) platformId: object) {
    this.isBrowser = isPlatformBrowser(platformId);
  }

  public init(): void {
    if (!this.isBrowser || this.lenis) {
      return;
    }

    this.lenis = new Lenis({
      duration: 1.2,
      easing: (t: number) => Math.min(1, 1.001 - Math.pow(2, -10 * t)),
      orientation: 'vertical',
      gestureOrientation: 'vertical',
      smoothWheel: true,
      wheelMultiplier: 1,
      touchMultiplier: 2,
    });

    const raf = (time: number) => {
      this.lenis?.raf(time);
      this.rafId = requestAnimationFrame(raf);
    };

    this.rafId = requestAnimationFrame(raf);
  }

  public scrollTo(target: string | HTMLElement, options: { offset?: number; duration?: number } = {}): void {
    if (!this.isBrowser) return;

    if (!this.lenis) {
      this.init();
    }

    const defaultOffset = -70; // Header height offset
    const offset = options.offset !== undefined ? options.offset : defaultOffset;

    if (typeof target === 'string') {
      const element = document.querySelector(target) as HTMLElement;
      if (element && this.lenis) {
        this.lenis.scrollTo(element, { offset, duration: options.duration });
      }
    } else if (target && this.lenis) {
      this.lenis.scrollTo(target, { offset, duration: options.duration });
    }
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
