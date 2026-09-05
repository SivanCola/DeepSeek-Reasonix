import { useLayoutEffect, useMemo, useRef } from "react";

function bindCommand<Args extends unknown[], Result>(ref: { current: (...args: Args) => Result }) {
  return (...args: Args): Result => ref.current(...args);
}

/** Stable event authority retaining only the latest committed presentation. */
export function useTranscriptCommand<Args extends unknown[], Result>(command: (...args: Args) => Result) {
  const ref = useRef(command);
  useLayoutEffect(() => { ref.current = command; });
  // Binding in a separate scope is intentional: V8 otherwise shares render
  // contexts between memoized callbacks and chains obsolete selection rows.
  return useMemo(() => bindCommand(ref), []);
}
