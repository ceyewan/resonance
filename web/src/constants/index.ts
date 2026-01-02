/**
 * 前端常量定义
 *
 * 包含 API 契约、消息类型、存储键等常量
 * 所有需要前后端约定的契约值应在此定义
 */

// ==================== 存储键 ====================
export const STORAGE_KEYS = {
  AUTH: "resonance-auth-storage",
} as const;

// ==================== HTTP Header ====================
export const HTTP_HEADERS = {
  AUTHORIZATION: "Authorization",
  IDempotencyKey: "X-Idempotency-Key",
  CONTENT_TYPE: "Content-Type",
} as const;

// ==================== WebSocket ====================
export const WS_CONFIG = {
  /**
   * 心跳间隔（毫秒）
   * 客户端每 30s 发送一次心跳保活
   */
  HEARTBEAT_INTERVAL: 30000,

  /**
   * WebSocket URL 参数名
   * 连接时通过 URL 参数传递 token
   */
  TOKEN_PARAM: "token",

  /**
   * 重连延迟（毫秒）
   * 连接断开后等待重连的时间
   */
  RECONNECT_DELAY: 3000,

  /**
   * 最大重连次数
   * 超过此次数后停止自动重连
   */
  MAX_RECONNECT_ATTEMPTS: 5,
} as const;

// ==================== 消息类型 ====================
/**
 * WebSocket 消息类型
 * 与后端 packet.proto 中的 type 字段对应
 */
export const MESSAGE_TYPES = {
  TEXT: "text",
  IMAGE: "image",
  FILE: "file",
  SYSTEM: "system",
  AUDIO: "audio",
  VIDEO: "video",
} as const;

export type MessageType = (typeof MESSAGE_TYPES)[keyof typeof MESSAGE_TYPES];

// ==================== 会话类型 ====================
/**
 * 会话类型
 * 与后端 api.proto 中 SessionInfo.type 对应
 */
export const SESSION_TYPES = {
  /** 单聊 */
  DIRECT: 1,
  /** 群聊 */
  GROUP: 2,
  /** 系统通知 */
  SYSTEM: 3,
} as const;

export type SessionType = (typeof SESSION_TYPES)[keyof typeof SESSION_TYPES];

// ==================== 消息状态 ====================
/**
 * 消息发送状态
 */
export const MESSAGE_STATUS = {
  SENDING: "sending",
  SENT: "sent",
  FAILED: "failed",
} as const;

export type MessageStatus = (typeof MESSAGE_STATUS)[keyof typeof MESSAGE_STATUS];

// ==================== UI 常量 ====================
export const UI_CONFIG = {
  /** 侧边栏宽度（像素） */
  SIDEBAR_WIDTH: 320,

  /** 消息列表每页数量 */
  MESSAGES_PAGE_SIZE: 50,

  /** 会话列表每页数量 */
  SESSIONS_PAGE_SIZE: 30,

  /** 头像默认尺寸（像素） */
  AVATAR_SIZE: 40,

  /** 消息最大宽度（像素） */
  MESSAGE_MAX_WIDTH: 480,

  /** 输入框最大高度（像素） */
  INPUT_MAX_HEIGHT: 120,
} as const;

// ==================== 时间格式 ====================
/**
 * 时间格式化选项
 */
export const TIME_FORMAT = {
  /** 消息时间戳格式 */
  MESSAGE_TIME: {
    hour: "2-digit",
    minute: "2-digit",
  } as const,

  /** 消息完整日期时间格式 */
  MESSAGE_DATETIME: {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  } as const,

  /** 会话列表时间格式 */
  SESSION_TIME: {
    month: "short",
    day: "numeric",
  } as const,
} as const;

// ==================== 错误消息 ====================
export const ERROR_MESSAGES = {
  NETWORK_ERROR: "网络连接失败，请检查网络设置",
  AUTH_FAILED: "用户名或密码错误",
  REGISTER_FAILED: "注册失败，请重试",
  SESSION_LOAD_FAILED: "加载会话列表失败",
  MESSAGE_SEND_FAILED: "发送消息失败",
  MESSAGE_LOAD_FAILED: "加载历史消息失败",
  WEBSOCKET_DISCONNECTED: "连接已断开",
  INVALID_INPUT: "请输入有效内容",
} as const;

// ==================== 默认值 ====================
export const DEFAULTS = {
  /** 默认群组名称 */
  GROUP_NAME: "未命名群组",

  /** 默认系统通知名称 */
  SYSTEM_NAME: "系统通知",

  /** 消息临时 ID 前缀 */
  TEMP_ID_PREFIX: "temp-",
} as const;
