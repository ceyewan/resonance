import type { ChatEvent } from "@gen/common/v1/event_pb";
import type { InboxEvent } from "@gen/common/v1/view_pb";

import { reconcileFinalAgentStream } from "./agentStream";
import { applyEvent } from "./applier";

export async function reconcileWsEvent(event: ChatEvent): Promise<void> {
  await applyEvent(event);
  reconcileFinalAgentStream(event);
}

export async function reconcileInboxEvent(inboxEvent: InboxEvent): Promise<void> {
  if (inboxEvent.event === undefined) {
    return;
  }
  await applyEvent(inboxEvent.event);
  reconcileFinalAgentStream(inboxEvent.event);
}
