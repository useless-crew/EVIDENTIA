import { Component, HostListener, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { LenisService } from '../../core/services/lenis.service';
import { DmsStateService } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-navbar',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './navbar.component.html',
  styleUrls: ['./navbar.component.css']
})
export class NavbarComponent {
  mobileMenuOpen = false;
  isScrolled = false;

  private lenisService = inject(LenisService);
  private dms = inject(DmsStateService);

  @HostListener('window:scroll', [])
  onWindowScroll() {
    this.isScrolled = window.scrollY > 20;
  }

  toggleMobileMenu() {
    this.mobileMenuOpen = !this.mobileMenuOpen;
  }

  scrollToSection(targetId: string, event?: Event) {
    if (event) {
      event.preventDefault();
    }
    this.mobileMenuOpen = false;
    this.lenisService.scrollTo(targetId);
  }

  goToLogin(event?: Event) {
    if (event) {
      event.preventDefault();
    }
    this.mobileMenuOpen = false;
    this.dms.navigateTo('login');
  }

  goToLanding(event?: Event) {
    if (event) {
      event.preventDefault();
    }
    this.mobileMenuOpen = false;
    this.dms.navigateTo('landing');
  }
}


