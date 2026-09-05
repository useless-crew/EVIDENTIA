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
import { ScrollTrigger } from 'gsap/ScrollTrigger';
import { AnimationService } from '../services/animation.service';

export type RevealVariant = 'up' | 'fade' | 'clip' | 'scale';

/**
 * Scroll-triggered entrance animation.
 *
 *   <div appReveal>…</div>
 *   <div appReveal="clip" [revealDelay]="0.1">…</div>
 *   <div appReveal revealStagger=".card">…</div>
 *
 * With `revealStagger` the host stays put and its matching children animate in
 * sequence — that is what makes a grid read as composed rather than as a dozen
 * independent elements popping at once.
 */
@Directive({
  selector: '[appReveal]',
  standalone: true
})
export class RevealDirective implements AfterViewInit, OnDestroy {
  @Input('appReveal') variant: RevealVariant | '' = 'up';
  @Input() revealDelay = 0;
  @Input() revealStagger?: string;
  @Input() revealDistance = 28;
  /** Viewport position that fires the reveal, in ScrollTrigger `start` syntax. */
  @Input() revealStart = 'top 86%';

  private host = inject(ElementRef<HTMLElement>);
  private ngZone = inject(NgZone);
  private animation = inject(AnimationService);
  private trigger?: ScrollTrigger;

  ngAfterViewInit(): void {
    const el = this.host.nativeElement as HTMLElement;
    el.setAttribute('data-reveal', '');

    // No JS motion for reduced-motion users — the CSS resting state applies.
    if (this.animation.prefersReducedMotion) {
      el.classList.add('is-revealed');
      return;
    }

    this.animation.initScrollTrigger();

    this.ngZone.runOutsideAngular(() => {
      const targets: gsap.TweenTarget = this.revealStagger
        ? Array.from(el.querySelectorAll(this.revealStagger))
        : el;

      if (Array.isArray(targets) && targets.length === 0) {
        el.classList.add('is-revealed');
        return;
      }

      const from = this.fromVars();

      gsap.set(targets, from);

      const tween = gsap.to(targets, {
        ...this.toVars(),
        duration: 0.9,
        ease: 'power3.out',
        delay: this.revealDelay,
        stagger: this.revealStagger ? 0.09 : 0,
        onComplete: () => el.classList.add('is-revealed'),
        scrollTrigger: {
          trigger: el,
          start: this.revealStart,
          once: true
        }
      });

      this.trigger = tween.scrollTrigger;
    });
  }

  private fromVars(): gsap.TweenVars {
    switch (this.variant || 'up') {
      case 'fade':
        return { opacity: 0 };
      case 'clip':
        return { opacity: 0, yPercent: 100 };
      case 'scale':
        return { opacity: 0, scale: 0.94, y: this.revealDistance * 0.5 };
      case 'up':
      default:
        return { opacity: 0, y: this.revealDistance };
    }
  }

  private toVars(): gsap.TweenVars {
    switch (this.variant || 'up') {
      case 'fade':
        return { opacity: 1 };
      case 'clip':
        return { opacity: 1, yPercent: 0 };
      case 'scale':
        return { opacity: 1, scale: 1, y: 0 };
      case 'up':
      default:
        return { opacity: 1, y: 0 };
    }
  }

  ngOnDestroy(): void {
    this.trigger?.kill();
  }
}
