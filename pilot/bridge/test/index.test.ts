import { describe, expect, it, vi } from "vitest";

import {
  BRIDGE_READY_COMMAND,
  bridgeReadinessDescription,
  createBusinessCompactionHandler,
  executeTool,
  fetchManifest,
  createProviderBudgetGuard,
  readDashScopeProvider,
  readBoundedJSON,
  readEnvironment,
  registerDashScopeProvider,
  validateManifest,
} from "../src/index.js";

const capability = "x".repeat(64);

function environment() {
  return readEnvironment({
    RESONANCE_TOOL_BROKER_URL: "http://127.0.0.1:15094",
    RESONANCE_AGENT_CAPABILITY: capability,
    RESONANCE_AGENT_RUN_ID: "run-123",
    RESONANCE_AGENT_MAX_TOTAL_TOKENS: "20000",
    RESONANCE_AGENT_MAX_COST_MICROS: "100000",
    RESONANCE_AGENT_MAX_PROVIDER_CALLS: "8",
    DASHSCOPE_API_KEY: "test-dashscope-key",
    DASHSCOPE_BASE_URL:
      "https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
    DASHSCOPE_MODEL: "qwen3.8-max",
  });
}

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
}

const manifest = {
  profile_id: "user-assistant",
  profile_version: 1,
  expires_at: "2999-01-01T00:00:00Z",
  tools: [
    {
      name: "get_my_profile",
      label: "Get my profile",
      description: "Read my profile",
      input_schema: { type: "object", properties: {}, additionalProperties: false },
      risk: "ReadSelf",
      schema_version: 1,
    },
  ],
};

describe("trusted environment", () => {
  it("accepts only explicit loopback HTTP", () => {
    expect(environment().brokerURL.href).toBe("http://127.0.0.1:15094/");
    for (const brokerURL of [
      "https://127.0.0.1:15094",
      "http://localhost:15094",
      "http://10.0.0.2:15094",
      "http://user@127.0.0.1:15094",
      "http://127.0.0.1:15094/path",
    ]) {
      expect(() =>
        readEnvironment({
          RESONANCE_TOOL_BROKER_URL: brokerURL,
          RESONANCE_AGENT_CAPABILITY: capability,
          RESONANCE_AGENT_RUN_ID: "run-123",
          RESONANCE_AGENT_MAX_TOTAL_TOKENS: "20000",
          RESONANCE_AGENT_MAX_COST_MICROS: "100000",
          RESONANCE_AGENT_MAX_PROVIDER_CALLS: "8",
          DASHSCOPE_API_KEY: "test-dashscope-key",
          DASHSCOPE_BASE_URL:
            "https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
          DASHSCOPE_MODEL: "qwen3.8-max",
        }),
      ).toThrow(/endpoint is invalid/);
    }
  });

  it("registers only a validated DashScope OpenAI-compatible provider", () => {
    const provider = readDashScopeProvider({
      DASHSCOPE_API_KEY: "test-dashscope-key",
      DASHSCOPE_BASE_URL:
        "https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
      DASHSCOPE_MODEL: "qwen3.8-max",
    });
    const registerProvider = vi.fn();
    registerDashScopeProvider({ registerProvider } as never, provider);
    expect(registerProvider).toHaveBeenCalledOnce();
    expect(registerProvider).toHaveBeenCalledWith(
      "dashscope",
      expect.objectContaining({
        baseUrl: provider.baseUrl,
        apiKey: "$DASHSCOPE_API_KEY",
        api: "openai-completions",
        models: [expect.objectContaining({ id: "qwen3.8-max", reasoning: true })],
      }),
    );
  });

  it("rejects placeholder secrets and unsafe Provider URLs", () => {
    for (const candidate of [
      {
        DASHSCOPE_API_KEY: "replace-with-your-dashscope-api-key",
        DASHSCOPE_BASE_URL:
          "https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
        DASHSCOPE_MODEL: "qwen3.8-max",
      },
      {
        DASHSCOPE_API_KEY: "test-dashscope-key",
        DASHSCOPE_BASE_URL: "http://127.0.0.1/compatible-mode/v1",
        DASHSCOPE_MODEL: "qwen3.8-max",
      },
      {
        DASHSCOPE_API_KEY: "test-dashscope-key",
        DASHSCOPE_BASE_URL:
          "https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1?target=evil",
        DASHSCOPE_MODEL: "qwen3.8-max",
      },
    ]) {
      expect(() => readDashScopeProvider(candidate)).toThrow(/Provider environment is invalid/);
    }
  });
});

describe("provider budget guard", () => {
  it("caps each provider payload and decrements a pessimistic Attempt budget", () => {
    const abort = vi.fn();
    const guard = createProviderBudgetGuard({
      maxTotalTokens: 1000,
      maxCostMicros: 5000,
      maxProviderCalls: 2,
    });
    const context = {
      model: { cost: { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75 } },
      abort,
    };
    const first = guard(
      {
        payload: { model: "claude", max_tokens: 1000, messages: [{ role: "user", content: "hi" }] },
      },
      context,
    );
    expect(first).toMatchObject({ max_tokens: expect.any(Number) });
    expect((first as { max_tokens: number }).max_tokens).toBeLessThan(1000);
    expect(abort).not.toHaveBeenCalled();

    const second = guard(
      {
        payload: {
          model: "claude",
          max_tokens: 1000,
          messages: [{ role: "user", content: "again" }],
        },
      },
      context,
    );
    expect((second as { max_tokens: number }).max_tokens).toBe(0);
    expect(abort).toHaveBeenCalledTimes(1);
    expect(() => JSON.stringify(second)).toThrow(/BigInt/u);
  });

  it("caps the OpenAI reasoning max_completion_tokens field used by Qwen", () => {
    const abort = vi.fn();
    const guard = createProviderBudgetGuard({
      maxTotalTokens: 65_536,
      maxCostMicros: 10_000_000,
      maxProviderCalls: 1,
    });
    const guarded = guard(
      {
        payload: {
          model: "qwen3.8-max",
          max_completion_tokens: 32_768,
          messages: [{ role: "user", content: "hi" }],
        },
      },
      {
        model: { cost: { input: 100, output: 100, cacheRead: 100, cacheWrite: 100 } },
        abort,
      },
    );
    expect(guarded).toMatchObject({ max_completion_tokens: expect.any(Number) });
    expect((guarded as { max_completion_tokens: number }).max_completion_tokens).toBeGreaterThan(0);
    expect(abort).not.toHaveBeenCalled();
  });

  it("aborts before unsupported, exhausted, or unpriced provider requests", () => {
    const abort = vi.fn();
    const guard = createProviderBudgetGuard({
      maxTotalTokens: 10,
      maxCostMicros: 10,
      maxProviderCalls: 1,
    });
    const result = guard(
      { payload: { max_tokens: 100, messages: [{ role: "user", content: "too large" }] } },
      { model: { cost: { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75 } }, abort },
    );
    expect(result).toMatchObject({ max_tokens: 0 });
    expect(abort).toHaveBeenCalledTimes(1);
    expect(() => JSON.stringify(result)).toThrow(/BigInt/u);

    const ambiguousAbort = vi.fn();
    const ambiguous = createProviderBudgetGuard({
      maxTotalTokens: 1000,
      maxCostMicros: 5000,
      maxProviderCalls: 1,
    })(
      { payload: { max_tokens: 10, max_completion_tokens: 10, messages: [] } },
      {
        model: { cost: { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75 } },
        abort: ambiguousAbort,
      },
    );
    expect(ambiguousAbort).toHaveBeenCalledTimes(1);
    expect(() => JSON.stringify(ambiguous)).toThrow(/BigInt/u);
  });
});

describe("business compaction", () => {
  const event = {
    type: "session_before_compact",
    preparation: {
      firstKeptEntryId: "entry-9",
      messagesToSummarize: [{ role: "user", content: "Please check my profile", timestamp: 1 }],
      turnPrefixMessages: [],
      tokensBefore: 12_000,
      previousSummary: "The user asked an unresolved profile question.",
    },
    reason: "threshold",
    willRetry: false,
    signal: new AbortController().signal,
  };
  const usage = {
    input: 100,
    output: 50,
    cacheRead: 0,
    cacheWrite: 0,
    totalTokens: 150,
    cost: { input: 0.0001, output: 0.0002, cacheRead: 0, cacheWrite: 0, total: 0.0003 },
  };
  const model = { cost: { input: 1, output: 2, cacheRead: 0.1, cacheWrite: 1.25 } };

  it("owns compaction with the shared budget guard and disables provider retries", async () => {
    const abort = vi.fn();
    const guard = createProviderBudgetGuard({
      maxTotalTokens: 100_000,
      maxCostMicros: 1_000_000,
      maxProviderCalls: 2,
    });
    const complete = vi.fn(async (_model: unknown, context: unknown, options: any) => {
      expect(options.maxRetries).toBe(0);
      expect(options.cacheRetention).toBe("none");
      expect(options.signal).toBe(event.signal);
      const request = context as {
        systemPrompt: string;
        messages: Array<{ content: Array<{ text: string }> }>;
      };
      expect(request.systemPrompt).toContain("untrusted data");
      const encoded = JSON.parse(request.messages[0]!.content[0]!.text) as {
        previous_summary: string;
        conversation: string;
      };
      expect(encoded.previous_summary).toContain("unresolved profile question");
      expect(encoded.conversation).toContain("Please check my profile");

      const guarded = await options.onPayload(
        { model: "claude", max_tokens: 4096, messages: [{ role: "user", content: "summary" }] },
        model,
      );
      expect(guarded).toMatchObject({ max_tokens: expect.any(Number) });
      return {
        role: "assistant",
        content: [{ type: "text", text: "## Safe continuation\n- Profile lookup is pending." }],
        usage,
      };
    });
    const handler = createBusinessCompactionHandler(guard);

    const result = await handler(
      event as never,
      {
        model,
        modelRegistry: { complete },
        abort,
      } as never,
    );

    expect(result).toEqual({
      compaction: {
        summary: "## Safe continuation\n- Profile lookup is pending.",
        firstKeptEntryId: "entry-9",
        tokensBefore: 12_000,
        usage,
        details: { protocol: 1, kind: "resonance-business" },
      },
    });
    expect(abort).not.toHaveBeenCalled();
  });

  it("cancels instead of falling back when compaction would exceed the Attempt budget", async () => {
    const guard = createProviderBudgetGuard({
      maxTotalTokens: 100_000,
      maxCostMicros: 1_000_000,
      maxProviderCalls: 1,
    });
    guard(
      { payload: { max_tokens: 10, messages: [{ role: "user", content: "first call" }] } },
      { model, abort: vi.fn() },
    );
    const abort = vi.fn();
    const complete = vi.fn(async (_model: unknown, _context: unknown, options: any) => {
      const denied = await options.onPayload(
        { max_tokens: 4096, messages: [{ role: "user", content: "compaction" }] },
        model,
      );
      JSON.stringify(denied);
      throw new Error("provider dispatch should have been impossible");
    });
    const handler = createBusinessCompactionHandler(guard);

    await expect(
      handler(event as never, { model, modelRegistry: { complete }, abort } as never),
    ).resolves.toEqual({ cancel: true });
    expect(abort).toHaveBeenCalledTimes(1);
  });

  it("cancels on summarizer failure so Pi cannot use its unguarded default path", async () => {
    const handler = createBusinessCompactionHandler(
      createProviderBudgetGuard({
        maxTotalTokens: 100_000,
        maxCostMicros: 1_000_000,
        maxProviderCalls: 2,
      }),
    );
    await expect(
      handler(
        event as never,
        {
          model,
          modelRegistry: { complete: vi.fn(async () => Promise.reject(new Error("offline"))) },
          abort: vi.fn(),
        } as never,
      ),
    ).resolves.toEqual({ cancel: true });
  });
});

describe("manifest", () => {
  it("fetches with the capability and validates a closed schema", async () => {
    const fetcher = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe("http://127.0.0.1:15094/v1/manifest");
      expect(new Headers(init?.headers).get("authorization")).toBe(`Bearer ${capability}`);
      expect(init?.redirect).toBe("error");
      return jsonResponse(manifest);
    });
    await expect(fetchManifest(environment(), fetcher)).resolves.toEqual(manifest);
  });

  it("emits a bounded profile-bound readiness proof after manifest validation", () => {
    expect(BRIDGE_READY_COMMAND).toBe("resonance_bridge_ready");
    expect(JSON.parse(bridgeReadinessDescription(manifest))).toEqual({
      bridge_protocol: 1,
      profile_id: "user-assistant",
      profile_version: 1,
      tool_count: 1,
    });
  });

  it("rejects duplicate tools, open schemas, references, and unknown fields", () => {
    expect(() => validateManifest({ ...manifest, unknown: true })).toThrow();
    expect(() =>
      validateManifest({ ...manifest, tools: [manifest.tools[0], manifest.tools[0]] }),
    ).toThrow();
    expect(() =>
      validateManifest({
        ...manifest,
        tools: [{ ...manifest.tools[0], input_schema: { type: "object", properties: {} } }],
      }),
    ).toThrow(/closed object/);
    expect(() =>
      validateManifest({
        ...manifest,
        tools: [
          {
            ...manifest.tools[0],
            input_schema: {
              type: "object",
              properties: { value: { $ref: "https://evil/schema" } },
              additionalProperties: false,
            },
          },
        ],
      }),
    ).toThrow(/forbidden reference/);
  });

  it("accepts the closed schemas used by the iam-admin read-only registry", () => {
    const adminManifest = {
      ...manifest,
      profile_id: "iam-admin",
      tools: [
        manifest.tools[0],
        {
          name: "get_tenant_user",
          label: "Get tenant user",
          description: "Read one masked tenant user",
          input_schema: {
            type: "object",
            properties: {
              username: {
                type: "string",
                minLength: 1,
                maxLength: 64,
                pattern: "^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$",
              },
            },
            required: ["username"],
            additionalProperties: false,
          },
          risk: "ReadTenantPII",
          schema_version: 1,
        },
        {
          name: "list_tenant_users",
          label: "List tenant users",
          description: "List masked tenant users",
          input_schema: {
            type: "object",
            properties: { limit: { type: "integer", minimum: 1, maximum: 20 } },
            additionalProperties: false,
          },
          risk: "ReadTenantPII",
          schema_version: 1,
        },
      ],
    };
    expect(validateManifest(adminManifest)).toEqual(adminManifest);
  });
});

describe("execution", () => {
  it("preserves the Pi tool call id and never sends an identity argument implicitly", async () => {
    const fetcher = vi.fn<typeof fetch>(async (_input, init) => {
      expect(JSON.parse(String(init?.body))).toEqual({
        run_id: "run-123",
        tool_call_id: "call-9",
        tool_name: "get_my_profile",
        args: {},
      });
      return jsonResponse({
        status: "ok",
        call_id: "call-9",
        model_text: "Authenticated user profile: username=alice",
        display_summary: "Loaded your profile",
        data: { username: "alice" },
        is_error: false,
      });
    });
    const result = await executeTool(
      environment(),
      "get_my_profile",
      "call-9",
      {},
      undefined,
      fetcher,
    );
    expect(result.call_id).toBe("call-9");
  });

  it("maps HTTP errors to bounded structured statuses without reflecting a response body", async () => {
    const failed = vi.fn<typeof fetch>(async () =>
      jsonResponse({ error: "secret downstream stack" }, 500),
    );
    const failedResult = await executeTool(
      environment(),
      "get_my_profile",
      "call-1",
      {},
      undefined,
      failed,
    );
    expect(failedResult.status).toBe("retryable_error");
    expect(failedResult.is_error).toBe(true);
    expect(failedResult.model_text).not.toContain("secret downstream stack");
    const mismatch = vi.fn<typeof fetch>(async () =>
      jsonResponse({
        status: "ok",
        call_id: "call-other",
        model_text: "ok",
        display_summary: "ok",
        data: {},
        is_error: false,
      }),
    );
    await expect(
      executeTool(environment(), "get_my_profile", "call-1", {}, undefined, mismatch),
    ).rejects.toThrow(/mismatched call identity/);
  });

  it.each(["execution_pending", "executed"] as const)(
    "accepts the bounded IAM execution status %s",
    async (status) => {
      const fetcher = vi.fn<typeof fetch>(async () =>
        jsonResponse({
          status,
          call_id: "call-iam",
          model_text: status === "executed" ? "Receipt committed" : "Execution pending",
          display_summary: status,
          data: {},
          is_error: false,
        }),
      );
      await expect(
        executeTool(environment(), "set_tenant_member_status", "call-iam", {}, undefined, fetcher),
      ).resolves.toMatchObject({ status, is_error: false });
    },
  );
});

describe("response limits", () => {
  it("rejects oversized and non-JSON responses", async () => {
    await expect(readBoundedJSON(jsonResponse({ payload: "x".repeat(128) }), 16)).rejects.toThrow(
      /exceeds/,
    );
    await expect(
      readBoundedJSON(new Response("plain", { headers: { "content-type": "text/plain" } }), 1024),
    ).rejects.toThrow(/invalid response/);
  });
});
