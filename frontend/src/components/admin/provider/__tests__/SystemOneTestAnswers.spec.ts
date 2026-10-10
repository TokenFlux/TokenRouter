import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import SystemOneTestAnswers from '../SystemOneTestAnswers.vue'
import type { SystemOneTestPayload, SystemOneTestResult } from '../systemOneTest'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

describe('SystemOneTestAnswers', () => {
  it('空态解释三类结果，等待时显示计算状态', async () => {
    const wrapper = mount(SystemOneTestAnswers)
    expect(wrapper.get('[data-testid="decision-results-guide"]').text()).toContain('guideTitle')
    expect(wrapper.text()).toContain('guide.noul')
    expect(wrapper.text()).toContain('guide.choice')
    expect(wrapper.text()).toContain('guide.score')
    await wrapper.setProps({ running: true })
    expect(wrapper.get('[data-testid="decision-results-guide"]').text()).toContain('waitingTitle')
    wrapper.unmount()
  })

  it('按提交顺序解释三类答案，区分概率、置信度和连续评分', () => {
    const questions: SystemOneTestPayload['questions'] = {
      ready: { type: 'noul', instructions: 'Is the service healthy?', criteria: { true: 'All checks passed', false: 'A check failed' } },
      route: { type: 'choice', instructions: 'Where should it go?', criteria: { a: 'Manual queue', b: 'Automatic queue' } },
      quality: { type: 'score', instructions: 'Rate quality', criteria: ['Low', 'Medium', 'High'] }
    }
    const result: SystemOneTestResult = {
      model: 'jev-latest',
      answers: {
        quality: { type: 'score', score: 1.05, confidence: 0.92, legend: { 0: 'Calm', 1: 'Frustrated', 2: 'Very angry' }, probabilities: { 2: 0.05, 0: 0, 1: 0.95 } },
        route: { type: 'choice', choice: 'b', confidence: 0.4, probabilities: { a: 0.3, b: 0.7 } },
        ready: { type: 'noul', noul: 0.83 }
      },
      usage: { input_tokens: 10, output_tokens: 0 }
    }
    const wrapper = mount(SystemOneTestAnswers, { props: { questions, result, usageValid: true } })
    expect(wrapper.findAll('article').map(node => node.attributes('data-testid'))).toEqual(['decision-answer-ready', 'decision-answer-route', 'decision-answer-quality'])
    const noul = wrapper.get('[data-testid="decision-answer-ready"]')
    expect(noul.text()).toContain('Is the service healthy?')
    expect(noul.text()).toContain('83.00%')
    expect(noul.text()).toContain('17.00%')
    expect(noul.text()).toContain('A check failed')
    const choice = wrapper.get('[data-testid="decision-answer-route"]')
    expect(choice.findAll('[data-option]').map(node => node.attributes('data-option'))).toEqual(['b', 'a'])
    expect(choice.get('[data-option="b"]').text()).toContain('selectedOption')
    expect(choice.text()).toContain('Automatic queue')
    expect(choice.text()).toContain('40.00%')
    expect(choice.text()).toContain('70.00%')
    const score = wrapper.get('[data-testid="decision-answer-quality"]')
    expect(score.text()).toContain('1.05')
    expect(score.findAll('[data-option]').map(node => node.attributes('data-option'))).toEqual(['0', '1', '2'])
    expect(score.get('[data-option="1"]').text()).toContain('Frustrated')
    expect(score.text()).not.toContain('Medium')
    wrapper.unmount()
  })

  it('零概率和缺失用量分别呈现', () => {
    const wrapper = mount(SystemOneTestAnswers, { props: { result: { model: 'jev-latest', answers: { ready: { type: 'noul', noul: 0 } } }, usageValid: false } })
    expect(wrapper.text()).toContain('usageUnknown')
    expect(wrapper.get('[data-testid="decision-answer-ready"]').text()).toContain('0.00%')
    expect(wrapper.get('[data-testid="decision-answer-ready"]').text()).toContain('100.00%')
    wrapper.unmount()
  })
})
