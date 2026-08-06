// The Wails bridge, declared by hand.
//
// `wails generate module` would emit typed bindings, but that couples the
// frontend build to the Wails CLI being installed. The surface the lite shell
// uses is two calls and one event, so declaring it here keeps `pnpm build`
// working on a bare checkout. It must stay in step with app.go.

/** One UI-visible unit of a turn. Mirrors session.Frame in Go. */
export type Frame = {
  kind: "text" | "reasoning" | "tool" | "notice" | "usage" | "turn_done" | "ready";
  text?: string;
  tool?: string;
  level?: "info" | "warn";
  err?: string;
  cacheHitRate: number;
  cacheKnown: boolean;
};

/** Wails event name carrying frames. Mirrors FrameEvent in app.go. */
const FRAME_EVENT = "reasonix:frame";

type WailsWindow = {
  go?: { main?: { App?: { Send(input: string): Promise<void>; Running(): Promise<boolean> } } };
  runtime?: { EventsOn(name: string, cb: (...data: unknown[]) => void): () => void };
};

function bridge(): WailsWindow {
  return window as unknown as WailsWindow;
}

/** Subscribes to frames, returning an unsubscribe function. */
export function onFrame(handler: (frame: Frame) => void): () => void {
  const runtime = bridge().runtime;
  if (!runtime) {
    // Running in a plain browser (vite dev without the shell). The UI still
    // renders; it just never receives frames.
    return () => {};
  }
  return runtime.EventsOn(FRAME_EVENT, (...data: unknown[]) => {
    const frame = data[0] as Frame | undefined;
    if (frame) handler(frame);
  });
}

/** Runs one turn. Resolves when the turn finishes. */
export async function send(input: string): Promise<void> {
  const app = bridge().go?.main?.App;
  if (!app) throw new Error("shell is not attached");
  await app.Send(input);
}

/** Reports whether a turn is already in flight, for a reloaded webview. */
export async function running(): Promise<boolean> {
  const app = bridge().go?.main?.App;
  if (!app) return false;
  return app.Running();
}
