/** SystemOne 决策测试使用文本或结构化状态，同一请求可以包含多种问题。 */
export interface SystemOneTestPayload {
  state: string | object
  questions: Record<string, {
    type: 'noul' | 'choice' | 'score'
    instructions: string
    criteria?: Record<string, string> | string[]
  }>
}

/** 答案由后端 SystemOne 解析器校验，用量是否有效由 usage_valid 单独标记。 */
export interface SystemOneTestResult {
  model: string
  answers: Record<string, {
    type: 'noul' | 'choice' | 'score'
    noul?: number
    choice?: string
    score?: number
    confidence?: number
    probabilities?: Record<string, number>
    legend?: unknown
  }>
  usage?: { input_tokens: number; output_tokens: number }
}
