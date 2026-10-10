<template>
  <div
    class="space-y-4"
    data-testid="systemone-test-form"
  >
    <p class="input-hint">{{ t('admin.providers.decisionTest.hint') }}</p>
    <div class="space-y-2">
      <SettingsSegmented
        v-model="stateFormat"
        :options="stateFormats"
        :ariaLabel="t('admin.providers.decisionTest.stateFormat')"
        :disabled="disabled"
        block
      />
      <TextArea
        v-model="stateText"
        :label="t('admin.providers.decisionTest.state')"
        :disabled="disabled"
        rows="4"
        data-testid="systemone-state"
      />
    </div>
    <RuleListEditor
      :items="questions"
      :title="t('admin.providers.decisionTest.questions')"
      :add-label="t('admin.providers.decisionTest.addQuestion')"
      :item-label="index => t('admin.providers.decisionTest.questionNumber', { number: index + 1 })"
      :disabled="disabled"
      :min="1"
      variant="card"
      test-id="systemone-questions"
      @add="addQuestion"
      @remove="questions.splice($event, 1)"
    >
      <template #row="{ item, index }">
        <div class="space-y-3">
          <div class="space-y-1">
            <label
              class="input-label"
              :for="`decision-id-${index}`"
            >{{ t('admin.providers.decisionTest.questionId') }}</label>
            <input
              :id="`decision-id-${index}`"
              v-model="item.id"
              class="input"
              :disabled="disabled"
              :data-testid="`decision-id-${index}`"
            />
          </div>
          <Select
            v-model="item.type"
            :options="questionTypes"
            :disabled="disabled"
            :aria-label="t('admin.providers.decisionTest.questionType')"
            :data-testid="`decision-type-${index}`"
          />
          <TextArea
            v-model="item.instructions"
            :label="t('admin.providers.decisionTest.instructions')"
            :disabled="disabled"
            rows="2"
            :data-testid="`decision-instructions-${index}`"
          />
          <details
            v-if="item.type === 'noul'"
            class="space-y-2"
          >
            <summary class="cursor-pointer text-xs text-gray-500 dark:text-dark-400">{{ t('admin.providers.decisionTest.criteriaOptional') }}</summary>
            <TextArea
              v-model="item.trueDescription"
              :label="t('admin.providers.decisionTest.trueDescription')"
              :disabled="disabled"
              rows="2"
            />
            <TextArea
              v-model="item.falseDescription"
              :label="t('admin.providers.decisionTest.falseDescription')"
              :disabled="disabled"
              rows="2"
            />
          </details>
          <RuleListEditor
            v-else
            :items="item.options"
            :title="t(item.type === 'score' ? 'admin.providers.decisionTest.levels' : 'admin.providers.decisionTest.options')"
            :hint="item.type === 'score' ? t('admin.providers.decisionTest.levelsHint') : undefined"
            :disabled="disabled"
            :min="item.type === 'score' ? 2 : 1"
            :max="item.type === 'score' ? 10 : 255"
            :reorderable="item.type === 'score'"
            :test-id="`decision-options-${index}`"
            @add="item.options.push({ id: '', description: '' })"
            @remove="item.options.splice($event, 1)"
            @move="(from, to) => moveOption(item, from, to)"
          >
            <template #row="{ item: option, index: optionIndex }">
              <div class="space-y-2">
                <input
                  v-if="item.type === 'choice'"
                  v-model="option.id"
                  class="input"
                  :disabled="disabled"
                  :placeholder="t('admin.providers.decisionTest.optionId')"
                  :aria-label="t('admin.providers.decisionTest.optionId')"
                />
                <TextArea
                  v-model="option.description"
                  :label="item.type === 'score' ? t('admin.providers.decisionTest.levelNumber', { number: optionIndex }) : t('admin.providers.decisionTest.optionDescription')"
                  :disabled="disabled"
                  rows="2"
                />
              </div>
            </template>
          </RuleListEditor>
        </div>
      </template>
    </RuleListEditor>
    <p
      v-if="validation.error"
      class="text-xs text-red-500"
      role="alert"
    >
      {{ validation.error }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import type { SystemOneTestPayload } from './systemOneTest'

defineProps<{ disabled: boolean }>()
const emit = defineEmits<{ change: [payload: SystemOneTestPayload | null] }>()
const { t } = useI18n()
const stateFormat = ref('text')
const stateText = ref(t('admin.providers.decisionTest.defaultState'))
const stateFormats = computed(() => [
  { value: 'text', label: t('admin.providers.decisionTest.text') },
  { value: 'json', label: 'JSON' }
])
const questionTypes = computed(() => ['noul', 'choice', 'score'].map(value => ({
  value,
  label: t(`admin.providers.decisionTest.${value}`)
})))
type QuestionDraft = {
  id: string
  type: 'noul' | 'choice' | 'score'
  instructions: string
  trueDescription: string
  falseDescription: string
  options: Array<{ id: string; description: string }>
}
const questions = ref<QuestionDraft[]>([{
  id: 'available',
  type: 'noul',
  instructions: t('admin.providers.decisionTest.defaultQuestion'),
  trueDescription: '',
  falseDescription: '',
  options: [{ id: 'a', description: '' }, { id: 'b', description: '' }]
}])

// 每个问题 ID 在请求内唯一，删除后添加也不会覆盖其他问题。
function addQuestion() {
  let number = questions.value.length + 1
  while (questions.value.some(item => item.id === `question_${number}`)) number++
  questions.value.push({
    id: `question_${number}`,
    type: 'noul',
    instructions: '',
    trueDescription: '',
    falseDescription: '',
    options: [{ id: 'a', description: '' }, { id: 'b', description: '' }]
  })
}

// Score 的数组顺序决定等级含义。
function moveOption(question: QuestionDraft, from: number, to: number) {
  const [option] = question.options.splice(from, 1)
  question.options.splice(to, 0, option)
}

// 表单未通过校验时向父组件发送 null，单模型和批量运行共用这份请求。
const validation = computed<{ payload: SystemOneTestPayload | null; error: string }>(() => {
  const invalid = (key: string) => ({ payload: null, error: t(`admin.providers.decisionTest.${key}`) })
  let state: string | object = stateText.value
  if (stateFormat.value === 'json') {
    try {
      state = JSON.parse(stateText.value)
    } catch {
      return invalid('invalidState')
    }
    if (state === null || (typeof state !== 'string' && typeof state !== 'object')) return invalid('invalidState')
  }
  const entries: Array<[string, SystemOneTestPayload['questions'][string]]> = []
  const ids = new Set<string>()
  for (const item of questions.value) {
    const id = item.id.trim()
    if (!id || ids.has(id)) return invalid('invalidIds')
    ids.add(id)
    if (!item.instructions.trim()) return invalid('missingInstructions')
    const question: SystemOneTestPayload['questions'][string] = { type: item.type, instructions: item.instructions }
    if (item.type === 'choice') {
      const keys = item.options.map(option => option.id.trim())
      if (keys.some(key => !key) || new Set(keys).size !== keys.length) return invalid('invalidOptions')
      question.criteria = Object.fromEntries(item.options.map((option, index) => [keys[index], option.description]))
    } else if (item.type === 'score') {
      if (item.options.length < 2 || item.options.length > 10) return invalid('invalidLevels')
      question.criteria = item.options.map(option => option.description)
    } else {
      const criteria = Object.fromEntries([
        ['true', item.trueDescription],
        ['false', item.falseDescription]
      ].filter(([, value]) => value.trim()))
      if (Object.keys(criteria).length) question.criteria = criteria
    }
    entries.push([id, question])
  }
  return { payload: { state, questions: Object.fromEntries(entries) }, error: '' }
})
watch(validation, result => emit('change', result.payload), { immediate: true })
</script>
