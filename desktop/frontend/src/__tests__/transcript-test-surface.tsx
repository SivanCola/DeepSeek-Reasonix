import type { ComponentProps } from "react";
import { Transcript } from "../components/Transcript";

export function TranscriptTestSurface({
  viewportHeight,
  rowHeight,
  ...props
}: ComponentProps<typeof Transcript> & { viewportHeight: number; rowHeight: number }) {
  void viewportHeight;
  void rowHeight;
  return <Transcript {...props} />;
}
