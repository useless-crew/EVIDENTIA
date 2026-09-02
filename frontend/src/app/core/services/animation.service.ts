import { Injectable, NgZone, inject } from '@angular/core';
import gsap from 'gsap';

@Injectable({
  providedIn: 'root'
})
export class AnimationService {
  private ngZone = inject(NgZone);

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
