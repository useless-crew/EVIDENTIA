import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { LandingDataService } from '../../core/services/landing-data.service';
import { ProblemCard, FeaturePillar, ProcessStep, FaqItem } from '../../core/models/landing.model';
import { FaqAccordionComponent } from '../../components/faq-accordion/faq-accordion.component';

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

  constructor(private landingDataService: LandingDataService) {}

  ngOnInit(): void {
    this.problemCards = this.landingDataService.getProblemCards();
    this.featurePillars = this.landingDataService.getFeaturePillars();
    this.processSteps = this.landingDataService.getProcessSteps();
    this.faqList = this.landingDataService.getFaqList();
  }
}
