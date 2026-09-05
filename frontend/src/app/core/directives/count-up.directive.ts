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

/**
 * Counts a number up when it scrolls into view.
 *
 *   <span [appCountUp]="99.98" countSuffix="%" [countDecimals]="2"></span>
 *
 * The element's text is written directly rather than through a binding so the
 * ~60 updates a second never touch change detection.
 */
@Directive({
  selector: '[appCountUp]',
  standalone: true
})
export class CountUpDirective implements AfterViewInit, OnDestroy {
  @Input('appCountUp') target = 0;
  @Input() countDecimals = 0;
  @Input() countPrefix = '';
  @Input() countSuffix = '';
  @Input() countDuration = 2;

  private host = inject(ElementRef<HTMLElement>);
  private ngZone = inject(NgZone);
  private animation = inject(AnimationService);
  private trigger?: ScrollTrigger;

  ngAfterViewInit(): void {
    const el = this.host.nativeElement as HTMLElement;

    if (this.animation.prefersReducedMotion) {
      el.textContent = this.format(this.target);
      return;
    }

    this.animation.initScrollTrigger();

    this.ngZone.runOutsideAngular(() => {
      const counter = { value: 0 };
      el.textContent = this.format(0);

      const tween = gsap.to(counter, {
        value: this.target,
        duration: this.countDuration,
        ease: 'power2.out',
        onUpdate: () => {
          el.textContent = this.format(counter.value);
        },
        scrollTrigger: {
          trigger: el,
          start: 'top 92%',
          once: true
        }
      });

      this.trigger = tween.scrollTrigger;
    });
  }

  private format(value: number): string {
    const rounded = value.toFixed(this.countDecimals);
    const [whole, fraction] = rounded.split('.');
    const grouped = Number(whole).toLocaleString('en-IN');
    const body = fraction ? `${grouped}.${fraction}` : grouped;
    return `${this.countPrefix}${body}${this.countSuffix}`;
  }

  ngOnDestroy(): void {
    this.trigger?.kill();
  }
}
