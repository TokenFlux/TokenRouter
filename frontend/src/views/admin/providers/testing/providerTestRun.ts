import { reactive } from 'vue'
import { openProviderTestStream } from '@/api/admin/providers'
import type {
  SystemOneTestPayload,
  SystemOneTestPreview,
  SystemOneTestResult,
} from './systemOneTest'
import { systemOneInputKey } from './systemOneTest'
import type { ProviderTestProtocol } from './providerTestProtocols'

/**
 * providerTestRun.ts collects one provider connection test into reactive state shared by the
 * single-model dialog and batch detail view. Each request starts with cleared output and a
 * copied decision questions and image previews. Timing uses the browser performance clock, while
 * content, images, and completion events arrive independently from the server.
 *
 * The first terminal event decides success or failure so a trailing completion cannot replace
 * an earlier error. Streaming decoding carries incomplete text lines between reads and handles
 * newline-terminated data records individually. AbortError returns the run to idle and reports
 * cancellation to the batch scheduler; other transport failures become visible errors. Callers
 * own cancellation and must coordinate reuse of a run.
 */

/**
 * ProviderTestStatus identifies idle, pending, and terminal connection-test display states.
 */
export type ProviderTestStatus = 'idle' | 'connecting' | 'success' | 'error'
/**
 * ProviderTestLogTone selects presentation colors for informational and terminal test messages.
 */
export type ProviderTestLogTone = 'info' | 'muted' | 'success' | 'error'

/**
 * ProviderTestLogLine pairs a rendered message with its presentation tone.
 */
export interface ProviderTestLogLine {
  text: string // visible event or log text
  tone: ProviderTestLogTone // log message presentation tone
}

/**
 * ProviderTestImage describes an image returned by a provider test event.
 */
export interface ProviderTestImage {
  url: string // returned image resource URL
  mimeType?: string // optional image MIME type
}

/**
 * ProviderTestRun holds mutable output consumed by single and batch test views. The request
 * executor updates this object in place, and callers control whether an active run may be
 * reused.
 */
export interface ProviderTestRun {
  status: ProviderTestStatus // current test display state
  logLines: ProviderTestLogLine[] // ordered visible log entries
  replyText: string // accumulated response text
  errorMessage: string // terminal failure display message
  resolvedModel: string // model reported by upstream
  /**
   * Latency in milliseconds to the first non-decision text or image output carrying a nonempty URL;
   * null means no eligible output has been observed. Decision content can populate replyText
   * while this value remains null.
   */
  firstTokenMs: number | null
  /**
   * Terminal elapsed milliseconds; null means finish has not accepted a completion or error
   * event.
   */
  totalMs: number | null
  images: ProviderTestImage[] // received test image records
  decisionResult?: SystemOneTestResult | null // decoded SystemOne result object
  /**
   * Copied request questions label returned answers even if the editable form changes during
   * execution.
   */
  decisionQuestions?: SystemOneTestPayload['questions'] | null
  /**
   * Copied images and Box targets remain tied to this submission after the editable form changes.
   */
  decisionPreview?: SystemOneTestPreview | null
  /**
   * Submitted input identity lets the view label preserved results when the editable draft changes.
   */
  decisionInput?: string | null
  /**
   * True permits the result view to display usage, including valid zero token counts.
   */
  decisionUsageValid?: boolean
}

/**
 * ProviderTestRequestBody combines administrator wire fields with optional browser preview
 * metadata. The runner excludes decisionPreview before serializing the HTTP request.
 */
export interface ProviderTestRequestBody {
  /** The selected membership supplies model attributes; omission uses the default catalogue values. */
  group_id?: number
  model_id: string // requested provider model identifier
  prompt?: string // optional test prompt text
  systemone?: SystemOneTestPayload // structured decision test payload
  /**
   * Browser-only preview metadata is copied into the run and excluded from the HTTP request body.
   */
  decisionPreview?: SystemOneTestPreview
  test_type: 'text' | 'image' | 'decision' // requested output test category
  mode?: 'default' | 'compact' | 'legacy_compact' // optional OpenAI compact mode
  protocol?: ProviderTestProtocol // optional selectable endpoint protocol
}

/**
 * ProviderTestEvent describes the recognized payload fields of one provider-test data record.
 */
interface ProviderTestEvent {
  type: string // server test event discriminator
  text?: string // visible event or log text
  model?: string // reported upstream model identifier
  success?: boolean // server terminal success flag
  error?: string // server terminal failure message
  image_url?: string // returned image resource URL
  mime_type?: string // optional image MIME type
  data?: {
    response?: SystemOneTestResult // decoded decision result object
    usage_valid?: boolean // validated token usage flag
    duration_ms?: number // server-reported duration in milliseconds
  } // decision completion result data
}

/**
 * Translate resolves localized test messages with optional interpolation values.
 */
type Translate = (key: string, params?: Record<string, unknown>) => string

/**
 * createProviderTestRun creates fresh reactive output and nullable timing values for one
 * connection test.
 */
export function createProviderTestRun(): ProviderTestRun {
  return reactive({
    status: 'idle',
    logLines: [],
    replyText: '',
    errorMessage: '',
    resolvedModel: '',
    firstTokenMs: null,
    totalMs: null,
    images: [],
  })
}

/**
 * resetProviderTestRun clears output, timing, and decision metadata on the supplied reactive
 * run. Callers coordinate active requests before reuse because this reset does not cancel an
 * existing stream.
 */
export function resetProviderTestRun(run: ProviderTestRun) {
  run.status = 'idle'
  run.logLines = []
  run.replyText = ''
  run.errorMessage = ''
  run.resolvedModel = ''
  run.firstTokenMs = null
  run.totalMs = null
  run.images = []
  run.decisionResult = null
  run.decisionQuestions = null
  run.decisionPreview = null
  run.decisionInput = null
  run.decisionUsageValid = false
}

/**
 * executeProviderTest posts a test request and writes streamed observations into the supplied
 * run. Its boolean result distinguishes cancellation from a completed attempt, including failed
 * attempts. The caller owns the AbortSignal and prevents overlapping writers to the same run.
 *
 * The first success or error event fixes the terminal status, while subsequent content can
 * still append output. A decision request copies questions and images before dispatch so later
 * form edits do not relabel its answers. The decoder retains incomplete newline-delimited records between
 * reads; an unterminated final record is not dispatched. AbortError resets status to idle and
 * leaves collected output available.
 */
export async function executeProviderTest(options: {
  providerId: number // provider endpoint record identifier
  providerName: string // provider name for logs
  body: ProviderTestRequestBody // serialized provider test payload
  run: ProviderTestRun // mutable request output state
  signal: AbortSignal // caller-owned request cancellation signal
  t: Translate // localized test message resolver
}): Promise<boolean> {
  const { run, t } = options
  resetProviderTestRun(run)
  // Copy questions and image previews before dispatch so later form changes cannot alter the
  // description or target image attached to a completed answer.
  if (options.body.test_type === 'decision' && options.body.systemone) {
    run.decisionInput = systemOneInputKey(options.body.systemone)
    run.decisionQuestions = JSON.parse(JSON.stringify(options.body.systemone.questions))
    run.decisionPreview = options.body.decisionPreview
      ? JSON.parse(JSON.stringify(options.body.decisionPreview))
      : null
  }
  run.status = 'connecting'
  const startedAt = performance.now()
  /**
   * elapsed measures rounded milliseconds since this request began on the browser performance
   * clock.
   */
  const elapsed = () => Math.round(performance.now() - startedAt)

  /**
   * addLine appends a display log entry with the supplied text and tone. Omitting tone selects
   * muted, which callers use for ordinary progress messages without success or error emphasis.
   */
  const addLine = (text: string, tone: ProviderTestLogTone = 'muted') => {
    run.logLines.push({ text, tone })
  }

  /**
   * markFirstToken records the first observed text or image latency in milliseconds. Later
   * observations leave the existing measurement unchanged, including a valid measurement of
   * zero milliseconds.
   */
  const markFirstToken = () => {
    if (run.firstTokenMs == null) run.firstTokenMs = elapsed()
  }

  /**
   * finish records the first terminal status, elapsed duration, and matching log entry for the
   * current stream. It accepts a result only while the run is connecting. Error results also
   * populate the visible failure message, while success appends the completion message.
   *
   * The server may send a completion event after an earlier error event. Checking the current
   * status before changing it keeps that later event from replacing the first accepted outcome
   * or measuring a different terminal duration. Abort handling remains a separate path that can
   * return the run to idle.
   */
  const finish = (status: 'success' | 'error', message = '') => {
    if (run.status !== 'connecting') return
    run.status = status
    run.totalMs = elapsed()
    if (status === 'error') {
      run.errorMessage = message
      addLine(t('admin.providers.errorPrefix', { message }), 'error')
    } else {
      addLine(t('admin.providers.testCompleted'), 'success')
    }
  }

  /**
   * handleEvent applies one decoded event to the current run and delegates terminal ordering to
   * finish. Unknown event types are ignored; decision results are accepted only on a successful
   * completion while connecting.
   */
  const handleEvent = (event: ProviderTestEvent) => {
    switch (event.type) {
      case 'test_start':
        addLine(t('admin.providers.connectedToApi'), 'success')
        if (event.model) {
          run.resolvedModel = event.model
          addLine(t('admin.providers.usingModel', { model: event.model }))
        }
        addLine(
          options.body.test_type === 'image'
            ? t('admin.providers.sendingImageRequest')
            : options.body.test_type === 'decision'
              ? t('admin.providers.decisionTest.sending')
              : t('admin.providers.sendingTestMessage'),
        )
        break
      case 'content':
        if (event.text) {
          if (options.body.test_type !== 'decision') markFirstToken()
          run.replyText += event.text
        }
        break
      case 'image':
        if (event.image_url) {
          markFirstToken()
          run.images.push({ url: event.image_url, mimeType: event.mime_type })
          addLine(t('admin.providers.imageReceived', { count: run.images.length }), 'info')
        }
        break
      case 'status':
        if (event.text) addLine(event.text, 'info')
        break
      case 'test_complete':
        if (event.success) {
          if (options.body.test_type === 'decision' && run.status === 'connecting') {
            run.decisionResult = event.data?.response ?? null
            run.decisionUsageValid = event.data?.usage_valid === true
          }
          finish('success')
        } else {
          finish('error', event.error || 'Test failed')
        }
        break
      case 'error':
        finish('error', event.error || 'Unknown error')
        break
    }
  }

  addLine(t('admin.providers.startingTestForProvider', { name: options.providerName }), 'info')

  try {
    const { decisionPreview: _preview, ...requestBody } = options.body
    const decision = requestBody.test_type === 'decision' ? requestBody.systemone : undefined
    const usesImages =
      !!decision &&
      (Object.keys(decision.assets ?? {}).length > 0 ||
        Object.values(decision.questions).some((question) => question.type === 'box'))
    // Choosing images or Box in this administrator form explicitly opts this test into the
    // experimental upstream feature. Private image names and associations never leave the browser.
    const response = await openProviderTestStream(
      options.providerId,
      requestBody as Record<string, unknown>,
      { usesImages, signal: options.signal },
    )
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }
    const reader = response.body?.getReader()
    if (!reader) {
      throw new Error('No response body')
    }

    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      // Retain decoder state and the unfinished line between reads so split UTF-8 bytes and
      // split JSON records stay together.
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (!line.startsWith('data: ')) continue
        const jsonStr = line.slice(6).trim()
        if (!jsonStr) continue
        try {
          handleEvent(JSON.parse(jsonStr))
        } catch (e) {
          // A malformed record is logged and skipped so subsequent complete records can still
          // produce a terminal result.
          console.error('Failed to parse SSE event:', e)
        }
      }
    }

    // A clean transport end still needs a terminal application event; finish retains any result
    // already established by the server.
    finish('error', t('admin.providers.testDialog.streamEnded'))
    return true
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      // Cancellation keeps collected output available while returning a nonterminal status for
      // the caller to classify the stopped row.
      run.status = 'idle'
      return false
    }
    finish('error', error instanceof Error ? error.message : 'Unknown error')
    return true
  }
}
