/**
 * 时间处理工具函数
 */

/**
 * 时间格式化选项（无类型断言）
 */
export const TIME_FORMAT = {
  /** 消息时间戳格式 */
  MESSAGE_TIME: {
    hour: "2-digit",
    minute: "2-digit",
  } as Intl.DateTimeFormatOptions & { hour: string; minute: string },

  /** 消息完整日期时间格式 */
  MESSAGE_DATETIME: {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  } as Intl.DateTimeFormatOptions,

  /** 会话列表时间格式 */
  SESSION_TIME: {
    month: "short",
    day: "numeric",
  } as Intl.DateTimeFormatOptions,
} as const;

/**
 * 将 bigint 时间戳（秒级）格式化为时间字符串
 */
export function formatTimestamp(timestamp: bigint, options: Intl.DateTimeFormatOptions = TIME_FORMAT.MESSAGE_TIME): string {
  const date = new Date(Number(timestamp) * 1000);
  return date.toLocaleTimeString("zh-CN", options);
}

/**
 * 将 bigint 时间戳（秒级）格式化为日期字符串
 */
export function formatDate(timestamp: bigint, options: Intl.DateTimeFormatOptions = TIME_FORMAT.SESSION_TIME): string {
  const date = new Date(Number(timestamp) * 1000);
  return date.toLocaleDateString("zh-CN", options);
}

/**
 * 判断时间戳是否为今天
 */
export function isToday(timestamp: bigint): boolean {
  const date = new Date(Number(timestamp) * 1000);
  const now = new Date();
  return date.toDateString() === now.toDateString();
}

/**
 * 格式化会话时间（今天显示时间，否则显示日期）
 */
export function formatSessionTime(timestamp: bigint): string {
  if (isToday(timestamp)) {
    return formatTimestamp(timestamp, TIME_FORMAT.MESSAGE_TIME);
  }
  return formatDate(timestamp, TIME_FORMAT.SESSION_TIME);
}
