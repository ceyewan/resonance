import { FormEvent, useState } from "react";
import { useSendMessage } from "../../hooks/useSendMessage";
import { GlassInput } from "../../components/GlassInput";
import { SendHorizonal, Loader2 } from "lucide-react";

interface ComposerProps {
  sessionId: string;
}

export function Composer({ sessionId }: ComposerProps) {
  const [draft, setDraft] = useState("");
  const { send } = useSendMessage();
  const [isSending, setIsSending] = useState(false);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!draft.trim() || isSending) return;
    
    setIsSending(true);
    try {
      await send({ sessionId, content: draft });
      setDraft("");
    } finally {
      setIsSending(false);
    }
  };

  return (
    <form onSubmit={(e) => void onSubmit(e)} className="flex gap-3 items-center w-full relative z-10">
      <div className="flex-1 relative">
        <GlassInput 
          className="w-full"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="Message..."
          disabled={isSending}
        />
      </div>
      <button 
        type="submit" 
        disabled={!draft.trim() || isSending}
        className="shrink-0 w-11 h-11 rounded-full bg-[var(--color-primary)] text-white flex items-center justify-center disabled:opacity-50 disabled:cursor-not-allowed hover:bg-[var(--color-primary-hover)] transition-all shadow-[inset_0_1px_1px_rgba(255,255,255,0.4),0_4px_12px_rgba(0,0,0,0.2)]"
      >
        {isSending ? (
          <Loader2 className="w-5 h-5 animate-spin" />
        ) : (
          <SendHorizonal className="w-5 h-5 ml-[-2px]" />
        )}
      </button>
    </form>
  );
}
