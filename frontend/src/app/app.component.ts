import { Component, OnInit, OnDestroy, inject } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { TricolorBarComponent } from './components/tricolor-bar/tricolor-bar.component';
import { NavbarComponent } from './components/navbar/navbar.component';
import { FooterComponent } from './components/footer/footer.component';
import { LenisService } from './core/services/lenis.service';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [RouterOutlet, TricolorBarComponent, NavbarComponent, FooterComponent],
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css']
})
export class AppComponent implements OnInit, OnDestroy {
  title = 'EVIDENTIA';
  private lenisService = inject(LenisService);

  ngOnInit(): void {
    this.lenisService.init();
  }

  ngOnDestroy(): void {
    this.lenisService.destroy();
  }
}

