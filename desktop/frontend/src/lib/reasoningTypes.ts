// Shared adapter resolution exposed by model settings and the composer.
export interface ResolvedReasoningView {
  apiFormat: string;
  protocol: string;
  selected: string;
  effective?: string;
  default?: string;
  options: { id: string; name: string; description?: string }[];
  error?: string;
}
