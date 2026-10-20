import { configure } from "@testing-library/react";

configure({ asyncUtilTimeout: 30_000 });
if (typeof window !== "undefined" && !window.matchMedia) {
  window.matchMedia = query => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false });
}
