import { Component } from '@angular/core';

@Component({
  selector: 'app-tricolor-bar',
  standalone: true,
  template: `
    <div class="tricolor-strip" aria-hidden="true">
      <div class="strip saffron"></div>
      <div class="strip white"></div>
      <div class="strip green"></div>
    </div>
  `,
  styles: [`
    .tricolor-strip {
      display: flex;
      width: 100%;
      height: 4px;
    }
    .strip {
      flex: 1;
    }
    .saffron { background: var(--tricolor-saffron, #FF9933); }
    .white { background: var(--tricolor-white, #FFFFFF); }
    .green { background: var(--tricolor-green, #138808); }
  `]
})
export class TricolorBarComponent {}
