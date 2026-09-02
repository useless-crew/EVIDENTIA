import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { DmsStateService } from '../../core/services/dms-state.service';

@Component({
  selector: 'app-access-preview',
  standalone: true,
  imports: [CommonModule],
  templateUrl: './access-preview.component.html',
  styleUrls: ['./access-preview.component.css']
})
export class AccessPreviewComponent {
  dms = inject(DmsStateService);
}
