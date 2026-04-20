import { FormEvent, KeyboardEvent, useState } from "react";
import { Loader2, SendHorizonal } from "lucide-react";

import { useSendMessage } from "../../hooks/useSendMessage";

interface ComposerProps {
  sessionId: string;
}

export function Composer({ sessionId }: ComposerProps) {
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const { send } = useSendMessage();

  const submit = async () => {
    if (draft.trim() === "" || sending) {
      return;
    }

    setSending(true);
    try {
      await send({ sessionId, content: draft });
      setDraft("");
    } finally {
      setSending(false);
    }
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    void submit();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void submit();
    }
  };

  return (
    <form
      className="shrink-0 p-3 border-t border-[var(--color-border)] bg-[var(--glass-surface)] backdrop-blur-xl z-20 flex gap-3 items-end"
      onSubmit={onSubmit}
    >
      <div className="flex-1 relative bg-[var(--glass-input-bg)] rounded-[20px] border border-[var(--color-border)] focus-within:border-[var(--color-border)] focus-within:bg-[var(--glass-input-focus-bg)] transition-all">
        <textarea
          className="w-full bg-transparent px-4 py-3 text-[15px] text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] opacity-60 outline-none resize-none max-h-[120px] min-h-[44px]"
          disabled={sending}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Message..."
          rows={1}
          value={draft}
        />
      </div>
      <button
        className="shrink-0 w-[44px] h-[44px] rounded-full bg-[var(--color-primary)] text-[var(--color-text)] flex items-center justify-center disabled:opacity-50 disabled:bg-[var(--glass-surface-hover)] disabled:text-[var(--color-text-muted)] opacity-60 transition-colors shadow-[0_4px_12px_rgba(0,0,0,0.15)]"
        disabled={draft.trim() === "" || sending}
        type="submit"
      >
        {sending ? (
          <Loader2 className="w-5 h-5 animate-spin" />
        ) : (
          <SendHorizonal className="w-5 h-5 ml-[-2px]" />
        )}
      </button>
    </form>
  );
}
