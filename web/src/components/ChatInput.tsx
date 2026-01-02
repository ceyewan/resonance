import { useState, useRef, useEffect, FormEvent, KeyboardEvent } from "react";
import { cn } from "@/lib/cn";

interface ChatInputProps {
  disabled?: boolean;
  onSend: (content: string) => void;
  placeholder?: string;
}

/**
 * 聊天输入框组件
 * 支持多行输入、自动高度调整
 */
export function ChatInput({
  disabled = false,
  onSend,
  placeholder = "输入消息...",
}: ChatInputProps) {
  const [value, setValue] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // 自动调整高度
  useEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;

    textarea.style.height = "auto";
    const scrollHeight = textarea.scrollHeight;
    const newHeight = Math.min(scrollHeight, 120); // 最大高度 120px
    textarea.style.height = `${newHeight}px`;
  }, [value]);

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = value.trim();
    if (!trimmed || disabled) return;

    onSend(trimmed);
    setValue("");

    // 重置高度
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // Enter 发送，Shift + Enter 换行
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  return (
    <div className="border-t border-gray-200 bg-white px-4 py-3 dark:border-gray-700 dark:bg-gray-900">
      <form onSubmit={handleSubmit} className="flex items-end gap-2">
        {/* 附件按钮 */}
        <button
          type="button"
          disabled={disabled}
          className={cn(
            "flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full text-gray-500 transition-colors",
            "hover:bg-gray-100 hover:text-gray-700",
            "dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-300",
            "disabled:opacity-50 disabled:cursor-not-allowed",
          )}
          title="发送附件（即将推出）"
        >
          <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M15.172 7l-6.586 6.586a2 2 0 102.828 2.828l6.414-6.586a4 4 0 00-5.656-5.656l-6.415 6.585a6 6 0 108.486 8.486L20.5 13"
            />
          </svg>
        </button>

        {/* 输入框 */}
        <div className="min-w-0 flex-1">
          <textarea
            ref={textareaRef}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={placeholder}
            disabled={disabled}
            rows={1}
            className={cn(
              "max-h-[120px] w-full resize-none rounded-2xl border border-gray-300 px-4 py-2.5",
              "bg-gray-100 text-gray-900 placeholder-gray-500",
              "focus:border-sky-500 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20",
              "disabled:opacity-50 disabled:cursor-not-allowed",
              "dark:border-gray-600 dark:bg-gray-800 dark:text-white dark:placeholder-gray-500",
              "dark:focus:border-sky-400 dark:focus:bg-gray-700",
              "transition-colors",
            )}
            style={{ minHeight: "42px" }}
          />
        </div>

        {/* 表情按钮 */}
        <button
          type="button"
          disabled={disabled}
          className={cn(
            "flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full text-gray-500 transition-colors",
            "hover:bg-gray-100 hover:text-gray-700",
            "dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-300",
            "disabled:opacity-50 disabled:cursor-not-allowed",
          )}
          title="表情（即将推出）"
        >
          <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M14.828 14.828a4 4 0 01-5.656 0M9 10h.01M15 10h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            />
          </svg>
        </button>

        {/* 发送按钮 */}
        <button
          type="submit"
          disabled={disabled || !value.trim()}
          className={cn(
            "flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full transition-colors",
            !value.trim() || disabled
              ? "bg-gray-300 text-gray-500 dark:bg-gray-700 dark:text-gray-500"
              : "bg-sky-500 text-white hover:bg-sky-600 dark:bg-sky-600 dark:hover:bg-sky-500",
            "disabled:cursor-not-allowed",
          )}
          title="发送"
        >
          <svg className="h-5 w-5" fill="currentColor" viewBox="0 0 20 20">
            <path d="M10.894 2.553a1 1 0 00-1.788 0l-7 14a1 1 0 001.169 1.409l5-1.429A1 1 0 009 15.571V11a1 1 0 112 0v4.571a1 1 0 00.725.962l5 1.428a1 1 0 001.17-1.408l-7-14z" />
          </svg>
        </button>
      </form>
    </div>
  );
}
