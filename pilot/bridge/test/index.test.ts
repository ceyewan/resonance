import { afterEach, describe, expect, it, vi } from "vitest";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import pilotBridge from "../src/index.js";

afterEach(() => vi.unstubAllEnvs());
describe("Pilot chat bridge", () => {
  it("registers one model, no tools and an explicit zero-tool readiness proof", () => {
    vi.stubEnv("DASHSCOPE_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1");
    vi.stubEnv("DASHSCOPE_MODEL", "qwen3.8-max");
    vi.stubEnv("DASHSCOPE_API_KEY", "unit-test-key");
    vi.stubEnv("RESONANCE_AGENT_MAX_OUTPUT_TOKENS", "8192");
    const registerProvider = vi.fn();
    const registerCommand = vi.fn();
    const registerTool = vi.fn();
    pilotBridge({
      registerProvider,
      registerCommand,
      registerTool,
      on: vi.fn(),
    } as unknown as ExtensionAPI);
    expect(registerProvider).toHaveBeenCalledOnce();
    expect(registerTool).not.toHaveBeenCalled();
    expect(registerCommand.mock.calls[0]?.[1].description).toBe(
      '{"bridge_protocol":1,"tool_count":0}',
    );
  });
  it("rejects an insecure Provider endpoint", () => {
    vi.stubEnv("DASHSCOPE_BASE_URL", "http://example.com/v1");
    expect(() => pilotBridge({} as ExtensionAPI)).toThrow();
  });
});
