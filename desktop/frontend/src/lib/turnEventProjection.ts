import { asArray } from "./array";
import { app } from "./bridge";
import { recordFrontendDiagnostic } from "./frontendDiagnosticBridge";
import type { WireEvent } from "./types";

type WireHandler = (event: WireEvent) => void;

export class TurnEventProjector {
  private readonly sequenceByTab = new Map<string, number>();
  private readonly repairByTab = new Map<string, Promise<void>>();
  private readonly gapQueueByTab = new Map<string, WireEvent[]>();
  private readonly epochByTab = new Map<string, string>();
  private handler: WireHandler = () => {};

  bind(handler: WireHandler) { this.handler = handler; }
  unbind(handler: WireHandler) { if (this.handler === handler) this.handler = () => {}; }

  release(tabId: string) {
    this.sequenceByTab.delete(tabId);
    this.gapQueueByTab.delete(tabId);
    this.epochByTab.delete(tabId);
  }

  observeRuntime(tabId: string, runtimeEpoch: string | undefined, latest: number, replayAfter: number | undefined, active: boolean) {
    if (runtimeEpoch && runtimeEpoch !== this.epochByTab.get(tabId)) {
      this.epochByTab.set(tabId, runtimeEpoch);
      this.sequenceByTab.delete(tabId);
      this.gapQueueByTab.delete(tabId);
    }
    let projected = this.sequenceByTab.get(tabId);
    if (projected === undefined) {
      projected = active ? Math.min(replayAfter ?? latest, latest) : latest;
      this.sequenceByTab.set(tabId, projected);
    }
    if (latest > projected) this.requestReplay(tabId, projected, runtimeEpoch);
  }

  acceptLive(tabId: string, event: WireEvent, runtimeEpoch?: string): boolean {
    if (typeof event.seq !== "number" || event.seq <= 0) return true;
    let last = this.sequenceByTab.get(tabId) ?? 0;
    if (event.seq === 1 && last > 1) {
      this.sequenceByTab.delete(tabId);
      this.gapQueueByTab.delete(tabId);
      last = 0;
    }
    if (event.seq <= last) return false;
    if (event.seq > last + 1 && typeof app.TurnEventsForTab === "function") {
      const queued = this.gapQueueByTab.get(tabId) ?? [];
      queued.push(event);
      this.gapQueueByTab.set(tabId, queued);
      this.requestReplay(tabId, last, event.runtimeEpoch ?? runtimeEpoch);
      return false;
    }
    this.sequenceByTab.set(tabId, event.seq);
    return true;
  }

  private requestReplay(tabId: string, afterSeq: number, runtimeEpoch?: string) {
    if (typeof app.TurnEventsForTab !== "function" || this.repairByTab.has(tabId)) return;
    const repair = this.replayGap(tabId, afterSeq, runtimeEpoch)
      .catch((error) => recordFrontendDiagnostic("runtime", "turn-events-gap-repair-failed", {
        afterSeq: this.sequenceByTab.get(tabId) ?? afterSeq,
        error: error instanceof Error ? error.message : String(error),
      }))
      .finally(() => this.repairByTab.delete(tabId));
    this.repairByTab.set(tabId, repair);
  }

  private async replayGap(tabId: string, afterSeq: number, runtimeEpoch?: string) {
    let cursor = afterSeq;
    // A live event may arrive during replay, so consume the new gap in-place.
    for (let attempt = 0; attempt < 3; attempt += 1) {
      const envelopes = await app.TurnEventsForTab!(tabId, cursor);
      const queued = this.gapQueueByTab.get(tabId) ?? [];
      const liveBySeq = new Map(queued.filter((event) => typeof event.seq === "number").map((event) => [event.seq as number, event]));
      for (const envelope of asArray(envelopes).sort((a, b) => a.seq - b.seq)) {
        const durable = envelope?.event;
        if (!durable || typeof envelope.seq !== "number") continue;
        const live = liveBySeq.get(envelope.seq);
        this.handler({
          ...durable,
          ...(live ?? {}),
          turnId: envelope.turnId || durable.turnId,
          seq: envelope.seq,
          status: (envelope.status || durable.status) as WireEvent["status"],
          tabId,
          runtimeEpoch: live?.runtimeEpoch ?? envelope.runtimeEpoch ?? runtimeEpoch,
        });
      }
      this.gapQueueByTab.delete(tabId);
      for (const pending of queued) this.handler(pending);
      if (!this.gapQueueByTab.has(tabId)) return;
      cursor = this.sequenceByTab.get(tabId) ?? cursor;
    }
    recordFrontendDiagnostic("runtime", "turn-events-gap-repair-incomplete", {
      afterSeq: this.sequenceByTab.get(tabId) ?? cursor,
    });
  }
}
