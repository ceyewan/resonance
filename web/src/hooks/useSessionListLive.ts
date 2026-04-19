import { useLiveQuery } from "dexie-react-hooks";

import { getSessions } from "../db/repo";
import { toBigIntId } from "../lib/id";

export function useSessionListLive() {
  return useLiveQuery(async () => {
    const rows = await getSessions();
    return rows.sort((left, right) => {
      const tsDelta = toBigIntId(right.lastEventTs) - toBigIntId(left.lastEventTs);
      if (tsDelta > 0n) {
        return 1;
      }
      if (tsDelta < 0n) {
        return -1;
      }
      return left.sessionId.localeCompare(right.sessionId);
    });
  }, []);
}
