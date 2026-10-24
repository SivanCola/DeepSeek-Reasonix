import { configure } from "@testing-library/react";
import { afterEach } from "vitest";
import { forgetAskDrafts } from "./state/askdraft";

configure({ asyncUtilTimeout: 30_000 });
if (typeof window !== "undefined" && !window.matchMedia) {
  window.matchMedia = query => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false });
}

afterEach(forgetAskDrafts);
