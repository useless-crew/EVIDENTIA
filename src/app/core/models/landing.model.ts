export interface FaqItem {
  q: string;
  a: string;
  open?: boolean;
}

export interface ProblemCard {
  title: string;
  body: string;
  iconSvgPath: string;
}

export interface FeaturePillar {
  title: string;
  body: string;
  iconSvgPath: string;
}

export interface ProcessStep {
  stepNumber: number;
  title: string;
  description: string;
}
