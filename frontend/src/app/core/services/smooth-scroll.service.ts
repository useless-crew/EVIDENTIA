import { Injectable, inject } from '@angular/core';
import { LenisService } from './lenis.service';

@Injectable({
  providedIn: 'root'
})
export class SmoothScrollService {
  private lenisService = inject(LenisService);

  init(): void {
    this.lenisService.init();
  }

  scrollTo(target: string | number | HTMLElement, options?: any): void {
    this.lenisService.scrollTo(target, options);
  }

  resize(): void {
    this.lenisService.resize();
  }

  stop(): void {
    this.lenisService.stop();
  }

  start(): void {
    this.lenisService.start();
  }

  destroy(): void {
    this.lenisService.destroy();
  }
}

