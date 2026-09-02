import { Component, OnInit, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { LandingDataService } from '../../core/services/landing-data.service';
import { ProblemCard, FeaturePillar, ProcessStep, FaqItem } from '../../core/models/landing.model';
import { FaqAccordionComponent } from '../../components/faq-accordion/faq-accordion.component';
import { LenisService } from '../../core/services/lenis.service';
import { DmsStateService } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-landing-page',
  standalone: true,
  imports: [CommonModule, FaqAccordionComponent],
  templateUrl: './landing-page.component.html',
  styleUrls: ['./landing-page.component.css']
})
export class LandingPageComponent implements OnInit {
  problemCards: ProblemCard[] = [];
  featurePillars: FeaturePillar[] = [];
  processSteps: ProcessStep[] = [];
  faqList: FaqItem[] = [];

  private lenisService = inject(LenisService);
  private landingDataService = inject(LandingDataService);
  private dms = inject(DmsStateService);

  ngOnInit(): void {
    this.problemCards = this.landingDataService.getProblemCards();
    this.featurePillars = this.landingDataService.getFeaturePillars();
    this.processSteps = this.landingDataService.getProcessSteps();
    this.faqList = this.landingDataService.getFaqList();
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
    this.dms.screen.set('login');
  }
}


