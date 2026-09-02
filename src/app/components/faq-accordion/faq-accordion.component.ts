import { Component, Input } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FaqItem } from '../../core/models/landing.model';

@Component({
  selector: 'app-faq-accordion',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './faq-accordion.component.html',
  styleUrls: ['./faq-accordion.component.css']
})
export class FaqAccordionComponent {
  @Input() faqs: FaqItem[] = [];

  toggleFaq(index: number) {
    this.faqs = this.faqs.map((faq, i) => ({
      ...faq,
      open: i === index ? !faq.open : false
    }));
  }
}
