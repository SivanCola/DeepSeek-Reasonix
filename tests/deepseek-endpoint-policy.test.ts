import { describe, expect, it } from "vitest";
import {
  DEFAULT_DEEPSEEK_BASE_URL,
  resolveDeepSeekConnectionTestTarget,
  validateDeepSeekCredentialedEndpoint,
} from "../src/desktop/deepseek-endpoint-policy.js";

describe("DeepSeek credentialed endpoint policy", () => {
  it("defaults to the official DeepSeek endpoint", () => {
    expect(validateDeepSeekCredentialedEndpoint(undefined)).toEqual({
      ok: true,
      baseUrl: DEFAULT_DEEPSEEK_BASE_URL,
      isOfficialHost: true,
    });
  });

  it("rejects remote HTTP endpoints before credentials are sent", () => {
    expect(validateDeepSeekCredentialedEndpoint("http://api.example.com/v1")).toEqual({
      ok: false,
      message: "Endpoint must use HTTPS; HTTP is only allowed for localhost",
    });
  });

  it("allows localhost HTTP for local proxies", () => {
    expect(validateDeepSeekCredentialedEndpoint("http://127.0.0.1:8080/v1/")).toEqual({
      ok: true,
      baseUrl: "http://127.0.0.1:8080/v1",
      isOfficialHost: false,
    });
  });

  it("rejects endpoint URLs that embed credentials", () => {
    expect(validateDeepSeekCredentialedEndpoint("https://user:pass@api.deepseek.com")).toEqual({
      ok: false,
      message: "Endpoint URL must not include credentials",
    });
  });

  it("does not combine a saved default key with an unsaved custom endpoint", () => {
    const target = resolveDeepSeekConnectionTestTarget({
      requestedBaseUrl: "https://proxy.example.com/v1",
      resolvedEndpoint: { apiKey: "sk-default-key" },
    });

    expect(target).toEqual({
      ok: true,
      baseUrl: "https://proxy.example.com/v1",
      apiKey: undefined,
    });
  });

  it("does not derive the request target from saved endpoint config", () => {
    const target = resolveDeepSeekConnectionTestTarget({
      resolvedEndpoint: {
        baseUrl: "https://proxy.example.com/v1",
        apiKey: "sk-proxy-key",
      },
    });

    expect(target).toEqual({
      ok: true,
      baseUrl: DEFAULT_DEEPSEEK_BASE_URL,
      apiKey: undefined,
    });
  });

  it("reuses the saved key when the requested endpoint matches the resolved endpoint", () => {
    const target = resolveDeepSeekConnectionTestTarget({
      requestedBaseUrl: "https://proxy.example.com/v1/",
      resolvedEndpoint: {
        baseUrl: "https://proxy.example.com/v1",
        apiKey: "sk-proxy-key",
      },
    });

    expect(target).toEqual({
      ok: true,
      baseUrl: "https://proxy.example.com/v1",
      apiKey: "sk-proxy-key",
    });
  });

  it("uses an explicitly provided draft key for a draft endpoint", () => {
    const target = resolveDeepSeekConnectionTestTarget({
      requestedBaseUrl: "https://proxy.example.com/v1",
      requestedApiKey: " sk-draft-key ",
      resolvedEndpoint: { apiKey: "sk-default-key" },
    });

    expect(target).toEqual({
      ok: true,
      baseUrl: "https://proxy.example.com/v1",
      apiKey: "sk-draft-key",
    });
  });
});
