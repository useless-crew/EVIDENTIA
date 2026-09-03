import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { TestBed } from '@angular/core/testing';
import { AppComponent } from './app.component';

// This file pre-existed as Angular CLI's default scaffold spec, which
// imported a component named `App` that never actually existed in this
// codebase (the real export has always been `AppComponent`) and asserted
// on placeholder "Hello, evidentia" text no template here ever contained
// — it could not have compiled. Fixed to actually exercise the real
// AppComponent (now just a root <router-outlet> — see app.component.ts).
describe('AppComponent', () => {
  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
  });

  it('should create the app', () => {
    const fixture = TestBed.createComponent(AppComponent);
    const app = fixture.componentInstance;
    expect(app).toBeTruthy();
  });
});
