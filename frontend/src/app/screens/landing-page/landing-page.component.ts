import {
  Component,
  OnInit,
  AfterViewInit,
  OnDestroy,
  ElementRef,
  ViewChild,
  NgZone,
  inject
} from '@angular/core';
import { CommonModule } from '@angular/common';
import gsap from 'gsap';
import { ScrollTrigger } from 'gsap/ScrollTrigger';
import { LandingDataService } from '../../core/services/landing-data.service';
import { ProblemCard, FeaturePillar, ProcessStep, FaqItem } from '../../core/models/landing.model';
import { FaqAccordionComponent } from '../../components/faq-accordion/faq-accordion.component';
import { TricolorBarComponent } from '../../components/tricolor-bar/tricolor-bar.component';
import { NavbarComponent } from '../../components/navbar/navbar.component';
import { FooterComponent } from '../../components/footer/footer.component';
import { RevealDirective } from '../../core/directives/reveal.directive';
import { MagneticDirective } from '../../core/directives/magnetic.directive';
import { CountUpDirective } from '../../core/directives/count-up.directive';
import { LenisService } from '../../core/services/lenis.service';
import { AnimationService } from '../../core/services/animation.service';
import { DmsStateService } from '../../core/services/dms-state.service';

interface HeroStat {
  value: number;
  decimals: number;
  suffix: string;
  label: string;
}

@Component({
  selector: 'app-landing-page',
  standalone: true,
  imports: [
    CommonModule,
    FaqAccordionComponent,
    TricolorBarComponent,
    NavbarComponent,
    FooterComponent,
    RevealDirective,
    MagneticDirective,
    CountUpDirective
  ],
  templateUrl: './landing-page.component.html',
  styleUrls: ['./landing-page.component.css']
})
export class LandingPageComponent implements OnInit, AfterViewInit, OnDestroy {
  problemCards: ProblemCard[] = [];
  featurePillars: FeaturePillar[] = [];
  processSteps: ProcessStep[] = [];
  faqList: FaqItem[] = [];

  readonly heroStats: HeroStat[] = [
    { value: 100, decimals: 0, suffix: '%', label: 'Documents hashed on upload' },
    { value: 256, decimals: 0, suffix: '-bit', label: 'SHA fingerprint per file' },
    { value: 0, decimals: 0, suffix: '', label: 'Silent edits possible' },
    { value: 65, decimals: 0, suffix: 'B', label: 'Evidence Act aligned' }
  ];

  @ViewChild('heroCard') heroCardRef?: ElementRef<HTMLElement>;
  @ViewChild('timeline') timelineRef?: ElementRef<HTMLElement>;
  @ViewChild('timelineProgress') timelineProgressRef?: ElementRef<HTMLElement>;

  private lenisService = inject(LenisService);
  private landingDataService = inject(LandingDataService);
  private animation = inject(AnimationService);
  private ngZone = inject(NgZone);
  private dms = inject(DmsStateService);

  private triggers: ScrollTrigger[] = [];
  private heroTimeline?: gsap.core.Timeline;

  ngOnInit(): void {
    this.problemCards = this.landingDataService.getProblemCards();
    this.featurePillars = this.landingDataService.getFeaturePillars();
    this.processSteps = this.landingDataService.getProcessSteps();
    this.faqList = this.landingDataService.getFaqList();
  }

  ngAfterViewInit(): void {
    if (typeof window === 'undefined' || this.animation.prefersReducedMotion) {
      return;
    }

    this.animation.initScrollTrigger();

    this.ngZone.runOutsideAngular(() => {
      this.playHeroIntro();
      this.buildHeroParallax();
      this.buildTimelineProgress();
      // Web fonts settle after first paint; stale trigger positions cause
      // reveals to fire early on a long page.
      ScrollTrigger.refresh();
    });
  }

  /** Staggered curtain reveal of the headline lines, then the supporting copy. */
  private playHeroIntro(): void {
    const lines = gsap.utils.toArray<HTMLElement>('.hero-line');
    const supporting = gsap.utils.toArray<HTMLElement>(
      '.hero-eyebrow, .hero-subtitle, .hero-cta-group, .hero-compliance-note'
    );

    this.heroTimeline = gsap
      .timeline({ defaults: { ease: 'power3.out' } })
      .from(lines, { yPercent: 118, opacity: 0, duration: 1.05, stagger: 0.08 })
      .from(supporting, { y: 18, opacity: 0, duration: 0.7, stagger: 0.07 }, '-=0.65')
      // Deliberately no `y` here: the scrub parallax below owns the card's y,
      // and two tweens writing the same property fight during the intro.
      .from('.fingerprint-card', { opacity: 0, scale: 0.96, duration: 1 }, '-=0.8')
      .from('.hero-card-halo', { opacity: 0, scale: 0.85, duration: 1.2 }, '<');
  }

  /** Hero card and its halo drift at different rates for depth on scroll. */
  private buildHeroParallax(): void {
    const card = this.heroCardRef?.nativeElement;
    if (!card) return;

    const tween = gsap.to(card, {
      y: -64,
      ease: 'none',
      scrollTrigger: {
        trigger: '#hero',
        start: 'top top',
        end: 'bottom top',
        scrub: 0.6
      }
    });
    this.collect(tween.scrollTrigger);

    const halo = gsap.to('.hero-card-halo', {
      y: 110,
      ease: 'none',
      scrollTrigger: {
        trigger: '#hero',
        start: 'top top',
        end: 'bottom top',
        scrub: 1.2
      }
    });
    this.collect(halo.scrollTrigger);
  }

  /** The timeline spine fills top-to-bottom in step with scroll progress. */
  private buildTimelineProgress(): void {
    const container = this.timelineRef?.nativeElement;
    const fill = this.timelineProgressRef?.nativeElement;
    if (!container || !fill) return;

    const tween = gsap.fromTo(
      fill,
      { scaleY: 0 },
      {
        scaleY: 1,
        ease: 'none',
        scrollTrigger: {
          trigger: container,
          start: 'top 72%',
          end: 'bottom 78%',
          scrub: 0.5
        }
      }
    );
    this.collect(tween.scrollTrigger);
  }

  private collect(trigger: ScrollTrigger | undefined): void {
    if (trigger) {
      this.triggers.push(trigger);
    }
  }

  padIndex(index: number): string {
    return String(index + 1).padStart(2, '0');
  }

  scrollToSection(targetId: string, event?: Event): void {
    if (event) {
      event.preventDefault();
    }
    this.lenisService.scrollTo(targetId);
  }

  goToLogin(event?: Event): void {
    if (event) {
      event.preventDefault();
    }
    this.dms.navigateTo('login');
  }

  ngOnDestroy(): void {
    this.heroTimeline?.kill();
    this.triggers.forEach((trigger) => trigger.kill());
    this.triggers = [];
  }
}
