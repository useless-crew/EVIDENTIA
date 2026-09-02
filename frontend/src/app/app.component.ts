import { Component, OnInit, inject, ElementRef, ViewChild, effect } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService } from './core/services/dms-state.service';
import { SmoothScrollService } from './core/services/smooth-scroll.service';
import { AnimationService } from './core/services/animation.service';

// Header & Sidebar Components
import { HeaderComponent } from './components/header/header.component';
import { SidebarComponent } from './components/sidebar/sidebar.component';
import { UploadModalComponent } from './components/upload-modal/upload-modal.component';

// Screen Components
import { LandingPageComponent } from './screens/landing-page/landing-page.component';
import { LoginComponent } from './screens/login/login.component';
import { DashboardComponent } from './screens/dashboard/dashboard.component';
import { CasesComponent } from './screens/cases/cases.component';
import { CaseDetailComponent } from './screens/case-detail/case-detail.component';
import { DocumentViewerComponent } from './screens/document-viewer/document-viewer.component';
import { RedactStudioComponent } from './screens/redact-studio/redact-studio.component';
import { AuditLogComponent } from './screens/audit-log/audit-log.component';
import { AccessPreviewComponent } from './screens/access-preview/access-preview.component';
import { AdminComponent } from './screens/admin/admin.component';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [
    CommonModule,
    HeaderComponent,
    SidebarComponent,
    UploadModalComponent,
    LandingPageComponent,
    LoginComponent,
    DashboardComponent,
    CasesComponent,
    CaseDetailComponent,
    DocumentViewerComponent,
    RedactStudioComponent,
    AuditLogComponent,
    AccessPreviewComponent,
    AdminComponent
  ],
  templateUrl: './app.component.html',
  styleUrls: ['./app.component.css']
})
export class AppComponent implements OnInit {
  dms = inject(DmsStateService);
  private scrollService = inject(SmoothScrollService);
  private animService = inject(AnimationService);

  @ViewChild('contentArea') contentArea!: ElementRef<HTMLElement>;

  constructor() {
    // Listen to screen changes and reset/resize Lenis smooth scroll
    effect(() => {
      const currentScreen = this.dms.screen();
      if (currentScreen) {
        this.scrollService.scrollTo(0, { immediate: true });
        setTimeout(() => {
          this.scrollService.resize();
        }, 100);
      }
    });
  }

  ngOnInit() {
    // Initialize Lenis smooth scrolling for whole website
    this.scrollService.init();
  }
}

