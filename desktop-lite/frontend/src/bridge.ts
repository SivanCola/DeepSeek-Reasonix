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

/** One command-palette entry. Mirrors session.Command in Go. */
export type Command = {
  id: string;
  title: string;
  subtitle?: string;
  enabled: boolean;
};

type WailsWindow = {
  go?: {
    main?: {
      App?: {
        Send(input: string): Promise<void>;
        Running(): Promise<boolean>;
        Ready(): Promise<boolean>;
        Commands(): Promise<Command[]>;
        RunCommand(id: string): Promise<string>;
      };
    };
  };
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

/**
 * Reports whether a conversation is open.
 *
 * The ready frame is a one-shot event the webview can miss by mounting after
 * assembly finished, so the UI polls this rather than trusting it was
 * listening at the right moment.
 */
export async function ready(): Promise<boolean> {
  const app = bridge().go?.main?.App;
  if (!app) return false;
  return app.Ready();
}

/**
 * Reads the palette catalog. Availability depends on whether a turn is running,
 * so this is re-read each time the palette opens rather than cached.
 */
export async function commands(): Promise<Command[]> {
  const app = bridge().go?.main?.App;
  if (!app) return [];
  return app.Commands();
}

/** Runs a palette command, returning the message to show (may be empty). */
export async function runCommand(id: string): Promise<string> {
  const app = bridge().go?.main?.App;
  if (!app) throw new Error("shell is not attached");
  return app.RunCommand(id);
}
