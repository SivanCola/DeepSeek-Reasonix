import { HttpError } from "./http_error";

export interface SideProgress {
  kind: "waiting" | "thinking" | "text" | "tool";
  text?: string;
  tool?: string;
}

const interrupted = () => new HttpError(502, "The side answer ended before completion", { code: "side.failed" });

export async function readSideAnswer(response: Response, progress?: (value: SideProgress) => void): Promise<string> {
  if (!response.body) throw interrupted();
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let held = "";
  let event = "";
  let data: string[] = [];
  const frame = (): string | undefined => {
    const name = event;
    const raw = data.join("\n");
    event = "";
    data = [];
    if (!raw) return;
    if (name === "progress") progress?.(JSON.parse(raw) as SideProgress);
    if (name === "done") {
      const value = JSON.parse(raw) as { answer: string };
      if (typeof value.answer !== "string") throw interrupted();
      return value.answer;
    }
    if (name === "error") {
      const value = JSON.parse(raw) as { status: number; code: string; error: string; params?: Record<string, string | number> };
      throw new HttpError(value.status, value.error, value);
    }
  };
  try {
    for (;;) {
      const { done, value } = await reader.read();
      held += decoder.decode(value, { stream: !done });
      let end: number;
      while ((end = held.indexOf("\n")) >= 0) {
        const line = held.slice(0, end).replace(/\r$/, "");
        held = held.slice(end + 1);
        if (line === "") {
          const answer = frame();
          if (answer !== undefined) return answer;
        } else if (line.startsWith("event:")) {
          event = line.slice(6).trimStart();
        } else if (line.startsWith("data:")) {
          data.push(line.slice(5).replace(/^ /, ""));
        }
      }
      if (done) throw interrupted();
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
