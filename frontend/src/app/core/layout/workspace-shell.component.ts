import { CommonModule } from '@angular/common';
import { Component, OnInit, effect, inject } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { DmsStateService } from '../services/dms-state.service';
import { SmoothScrollService } from '../services/smooth-scroll.service';
import { HeaderComponent } from '../../components/header/header.component';
import { SidebarComponent } from '../../components/sidebar/sidebar.component';
import { UploadModalComponent } from '../../components/upload-modal/upload-modal.component';

/**
 * The authenticated workspace chrome (header/sidebar/breadcrumb/footer/
 * upload modal) that every /app/** route renders inside — extracted
 * as-is from what used to be app.component.html's `.app-workspace` div
 * (see app.routes.ts's own comment), now hosting a real `<router-outlet>`
 * for its child routes instead of a `*ngIf` chain over a signal.
 */
@Component({
  selector: 'app-workspace-shell',
  standalone: true,
  imports: [CommonModule, RouterOutlet, HeaderComponent, SidebarComponent, UploadModalComponent],
  templateUrl: './workspace-shell.component.html',
  styleUrls: ['./workspace-shell.component.css'],
})
export class WorkspaceShellComponent implements OnInit {
  dms = inject(DmsStateService);
  private scrollService = inject(SmoothScrollService);

  constructor() {
    // Reset/resize Lenis smooth scroll on every in-app navigation — the
    // same behavior app.component.ts used to run on every `dms.screen()`
    // change, now driven by the same signal (DmsStateService keeps it in
    // sync with the Router — see that service's constructor).
    effect(() => {
      const currentScreen = this.dms.screen();
      if (currentScreen) {
        this.scrollService.scrollTo(0, { immediate: true });
        setTimeout(() => this.scrollService.resize(), 100);
      }
    });
  }

  ngOnInit() {
    this.scrollService.init();
  }
}
