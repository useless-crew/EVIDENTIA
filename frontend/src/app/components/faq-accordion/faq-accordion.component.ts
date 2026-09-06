import { Component, Input, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FaqItem } from '../../core/models/landing.model';
import { AnimationService } from '../../core/services/animation.service';

@Component({
  selector: 'app-faq-accordion',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './faq-accordion.component.html',
  styleUrls: ['./faq-accordion.component.css']
})
export class FaqAccordionComponent {
  @Input() faqs: FaqItem[] = [];

  private animation = inject(AnimationService);

  toggleFaq(index: number) {
    this.faqs = this.faqs.map((faq, i) => ({
      ...faq,
      open: i === index ? !faq.open : false
    }));

    // Opening an answer changes the page height, which leaves every
    // ScrollTrigger below this point measuring against a stale layout.
    // Refresh once the expanded answer has actually been laid out.
    requestAnimationFrame(() => this.animation.refresh());
  }
}
