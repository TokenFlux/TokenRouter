/**
 * UI 交互时长的共享常量(ms)。
 * 同一语义的时长只在这里维护一份,调用方需要差异时按参数覆盖,不另起字面量。
 */

/** “已复制”反馈的展示时长;所有复制按钮/复制行共用。 */
export const COPY_FEEDBACK_MS = 2000

/** 列表搜索输入的防抖时长;各列表页搜索框共用。 */
export const SEARCH_DEBOUNCE_MS = 300
