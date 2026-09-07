import { Component, OnInit, inject } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { SmoothScrollService } from './core/services/smooth-scroll.service';

/**
 * The application root — now just the top-level route outlet.
 * landing/login render on their own; the authenticated workspace chrome
 * (header/sidebar/upload modal) moved to
 * core/layout/workspace-shell.component.ts, which /app/** routes render
 * inside (see app.routes.ts). The per-navigation scroll-reset effect that
 * used to live here (keyed on `dms.screen()`) now lives in
 * WorkspaceShellComponent, the only place that still needs it; Lenis's
 * own init() stays here too since it's idempotent (LenisService guards
 * against a second init) and the public landing page needs smooth scroll
 * active before any /app route ever loads.
 */
@Component({
  selector: 'app-root',
  standalone: true,
  imports: [RouterOutlet],
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css'],
})
export class AppComponent implements OnInit {
  private scrollService = inject(SmoothScrollService);

  ngOnInit() {
    this.scrollService.init();
  }
}
