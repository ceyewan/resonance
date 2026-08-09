import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

// Minimal extension for the opt-in live Provider contract test. It deliberately
// excludes Tool Broker wiring so Provider/SDK compatibility can be diagnosed
// independently from the full trusted Bridge lifecycle.
export default async function dashScopeProviderSmoke(pi: ExtensionAPI): Promise<void> {
  const source = process.env.RESONANCE_BRIDGE_SOURCE ?? "../../src/index.js";
  const { createProviderBudgetGuard, readDashScopeProvider, registerDashScopeProvider } =
    await import(source);
  registerDashScopeProvider(pi, readDashScopeProvider(process.env));
  if (process.env.RESONANCE_SMOKE_BUDGET === "1") {
    const guard = createProviderBudgetGuard({
      maxTotalTokens: Number(process.env.RESONANCE_AGENT_MAX_TOTAL_TOKENS),
      maxCostMicros: Number(process.env.RESONANCE_AGENT_MAX_COST_MICROS),
      maxProviderCalls: Number(process.env.RESONANCE_AGENT_MAX_PROVIDER_CALLS),
    });
    pi.on("before_provider_request", (event, context) => {
      const payload = event.payload as Record<string, unknown>;
      console.error(
        JSON.stringify({
          providerBudgetProbe: true,
          payloadBytes: Buffer.byteLength(JSON.stringify(payload)),
          keys: Object.keys(payload).sort(),
          maxTokens: payload.max_tokens,
          maxCompletionTokens: payload.max_completion_tokens,
        }),
      );
      return guard(event, context);
    });
  }
}
