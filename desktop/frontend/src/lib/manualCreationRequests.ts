import { app } from "./bridge";
import type { ManualSessionCreationRequest, ManualSessionCreationView } from "../generated/desktopContract.generated";

// Unacknowledged requests remain retryable with the same identity even when
// the creation-intent database could not be written. They are not successes.
const failed = new Map<string,{request:ManualSessionCreationRequest;error:string}>();
const listeners = new Set<()=>void>();
let version=0;
const notify=()=>{version++;listeners.forEach(listener=>listener());};
export const manualCreationFailures=()=>[...failed.values()];
export const manualCreationSnapshot=()=>version;
export const subscribeManualCreationFailures=(listener:()=>void)=>{listeners.add(listener);return()=>{listeners.delete(listener);};};
export function acknowledgeManualCreation(id:string){if(failed.delete(id))notify();}
export async function beginManualCreation(request:ManualSessionCreationRequest) {
 try {const result=await app.BeginManualSessionCreation(request);acknowledgeManualCreation(request.operationId);return result;}
 catch(error){failed.set(request.operationId,{request,error:String(error)});notify();throw error;}
}

export async function createManualSession(request: ManualSessionCreationRequest, options?: {
 onSurfaceReady: (operation: ManualSessionCreationView) => Promise<void>;
 isObservationCurrent?: () => boolean;
}) {
 let operation;
 try { operation = await beginManualCreation(request); }
 catch (error) {
   // A lost response is resolved against the original persisted identity.
   try { operation = await app.GetManualSessionCreation(request.operationId); acknowledgeManualCreation(request.operationId); }
   catch { throw error; }
 }
 let surfaceOpened = false;
 while (true) {
   // Acknowledgement transfers creation to the host. Leaving this surface
   // stops only its observer, never the durable operation or its recovery.
   if (options?.isObservationCurrent?.() === false) return operation;
   // The host publishes a formal session before constructing its runtime. Open
   // that identity once so typing can start; completion must never reselect it.
   // Older hosts only publish a usable surface at ready.
   if (!surfaceOpened && (operation.surfaceReady || operation.phase === "ready")) {
     surfaceOpened = true;
     await options?.onSurfaceReady(operation);
   }
   const status = operation.progress?.status;
   const recovering = status && ["queued", "running", "waiting_lock", "waiting_workspace", "retrying_storage"].includes(status);
   if (options?.isObservationCurrent?.() === false || operation.phase === "ready" || status === "blocked" || status === "stopped" || status === "stopping"
     || (!recovering && operation.phase !== "reserved" && operation.phase !== "starting")) return operation;
   await new Promise(resolve => setTimeout(resolve, 150));
   if (options?.isObservationCurrent?.() === false) return operation;
   operation = await app.GetManualSessionCreation(request.operationId);
 }
}
