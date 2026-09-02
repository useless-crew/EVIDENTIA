import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService, H_RED } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-redact-studio',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './redact-studio.component.html',
  styleUrls: ['./redact-studio.component.css']
})
export class RedactStudioComponent {
  dms = inject(DmsStateService);
  readonly redactHash = H_RED;

  private canvasBox: DOMRect | null = null;
  private isDrawing = false;

  onMouseDown(e: MouseEvent) {
    const el = e.currentTarget as HTMLElement;
    this.canvasBox = el.getBoundingClientRect();
    const x = e.clientX - this.canvasBox.left;
    const y = e.clientY - this.canvasBox.top;
    this.isDrawing = true;
    this.dms.startDraft(x, y);
  }

  onMouseMove(e: MouseEvent) {
    if (!this.isDrawing || !this.canvasBox) return;
    const x = e.clientX - this.canvasBox.left;
    const y = e.clientY - this.canvasBox.top;
    this.dms.updateDraft(x, y);
  }

  onMouseUp() {
    if (!this.isDrawing) return;
    this.isDrawing = false;
    this.dms.endDraft();
  }

  removeRegion(id: number) {
    this.dms.removeRedaction(id);
  }

  saveCopy() {
    this.dms.saveRedactedCopy();
  }

  cancel() {
    this.dms.navigateTo('doc');
  }
}
