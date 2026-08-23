/**
 * Bounded, session-aware transcript geometry cache.
 *
 * Exact samples are keyed by session + row + layout state + content version +
 * readable width + typography. Samples never calibrate another logical row;
 * an unseen row always uses its own state-aware static estimate.
 */

import {
  estimateTranscriptRowGeometry,
  transcriptRowLayoutVariant,
  type TranscriptEstimateSource,
  type TranscriptGeometryEnvironment,
  type TranscriptRowLayoutVariant,
} from "./transcriptRowGeometry";
import { transcriptRowMeasurementVersion, type TranscriptRow } from "./transcriptRows";

const DEFAULT_SESSION_CAP = 8;
const DEFAULT_ROW_CAP = 4_096;

type GeometrySample = {
  rowKey: string;
  kind: TranscriptRow["kind"];
  layoutVariant: TranscriptRowLayoutVariant;
  height: number;
  contentWidth?: number;
  typographySignature: string;
  measurementVersion: string;
  staticEstimate?: number;
};

type SessionMeasurements = { rows: Map<string, GeometrySample> };

export type TranscriptGeometryRecord = {
  rowKey: string;
  kind: TranscriptRow["kind"];
  layoutVariant: TranscriptRowLayoutVariant;
  height: number;
  environment: TranscriptGeometryEnvironment;
  measurementVersion: string;
  staticEstimate?: number;
};

export type TranscriptSynthesizedSizes = {
  heightEstimates: number[];
  estimateSources: TranscriptEstimateSource[];
};

export type TranscriptMeasuredSizes = {
  recordGeometry: (sessionKey: string, record: TranscriptGeometryRecord) => void;
  synthesizeDetailed: (
    sessionKey: string,
    rows: readonly TranscriptRow[],
    environment: TranscriptGeometryEnvironment,
  ) => TranscriptSynthesizedSizes;
};

export type TranscriptMeasuredSizesOptions = {
  maxSessions?: number;
  maxRowsPerSession?: number;
};

function normalizedWidth(width: number | undefined): number | undefined {
  return Number.isFinite(width) && (width ?? 0) > 0 ? width : undefined;
}

function environmentMatches(sample: GeometrySample, environment: TranscriptGeometryEnvironment): boolean {
  if (sample.typographySignature !== environment.typographySignature) return false;
  const width = normalizedWidth(environment.contentWidth);
  return width === undefined || (sample.contentWidth !== undefined && Math.abs(sample.contentWidth - width) <= 1);
}

export function createTranscriptMeasuredSizes(options: TranscriptMeasuredSizesOptions = {}): TranscriptMeasuredSizes {
  const maxSessions = Math.max(1, Math.round(options.maxSessions ?? DEFAULT_SESSION_CAP));
  const maxRowsPerSession = Math.max(1, Math.round(options.maxRowsPerSession ?? DEFAULT_ROW_CAP));
  const sessions = new Map<string, SessionMeasurements>();

  const touchSession = (sessionKey: string): SessionMeasurements => {
    const key = sessionKey || "__default__";
    const existing = sessions.get(key);
    const session = existing ?? { rows: new Map<string, GeometrySample>() };
    if (existing) sessions.delete(key);
    sessions.set(key, session);
    while (sessions.size > maxSessions) {
      const oldest = sessions.keys().next().value as string | undefined;
      if (oldest === undefined) break;
      sessions.delete(oldest);
    }
    return session;
  };

  const storeRecord = (sessionKey: string, record: Omit<GeometrySample, "contentWidth" | "typographySignature"> & {
    environment: TranscriptGeometryEnvironment;
  }) => {
    if (!Number.isFinite(record.height) || record.height <= 0) return;
    const session = touchSession(sessionKey);
    const sample: GeometrySample = {
      rowKey: record.rowKey,
      kind: record.kind,
      layoutVariant: record.layoutVariant,
      height: record.height,
      contentWidth: normalizedWidth(record.environment.contentWidth),
      typographySignature: record.environment.typographySignature,
      measurementVersion: record.measurementVersion,
      staticEstimate: record.staticEstimate,
    };
    // One latest observation per logical row; animation frames never add weight.
    session.rows.delete(record.rowKey);
    session.rows.set(record.rowKey, sample);
    while (session.rows.size > maxRowsPerSession) {
      const oldest = session.rows.keys().next().value as string | undefined;
      if (oldest === undefined) break;
      session.rows.delete(oldest);
    }
  };

  const synthesizeDetailed: TranscriptMeasuredSizes["synthesizeDetailed"] = (sessionKey, rows, environment) => {
    const session = touchSession(sessionKey);
    // A late-content patch invalidates that row immediately.
    for (const row of rows) {
      const rowKey = String(row.key);
      const sample = session.rows.get(rowKey);
      if (sample && sample.measurementVersion !== transcriptRowMeasurementVersion(row)) session.rows.delete(rowKey);
    }

    const heightEstimates: number[] = [];
    const estimateSources: TranscriptEstimateSource[] = [];
    for (const row of rows) {
      const rowKey = String(row.key);
      const layoutVariant = transcriptRowLayoutVariant(row);
      const measurementVersion = transcriptRowMeasurementVersion(row);
      const staticEstimate = estimateTranscriptRowGeometry(row, environment);
      const exact = session.rows.get(rowKey);
      if (
        exact
        && exact.kind === row.kind
        && exact.layoutVariant === layoutVariant
        && exact.measurementVersion === measurementVersion
        && environmentMatches(exact, environment)
      ) {
        heightEstimates.push(exact.height);
        estimateSources.push("exact");
        continue;
      }

      heightEstimates.push(staticEstimate);
      estimateSources.push("static");
    }
    return { heightEstimates, estimateSources };
  };

  return {
    recordGeometry: (sessionKey, record) => storeRecord(sessionKey, record),
    synthesizeDetailed,
  };
}
