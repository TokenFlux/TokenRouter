import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import {
  directive,
  nodes,
  parseScript,
  parseVue,
  readSource,
} from '@/__tests__/helpers/sourceSyntax'

// groupsSchedulerType.spec.ts checks scheduler selection in group drafts and
// settings controls. Create and edit modes share GroupFormModal, begin with the
// basic scheduler, restore saved group selections, and configure advanced
// overrides through the shared override dialog.

// Parsed components and draft module cover modal state, draft defaults, and the
// shared form control together.
const page = parseVue('views/admin/GroupsView.vue')
const modal = parseVue('views/admin/groups/GroupFormModal.vue')
const draftScript = parseScript(readSource('views/admin/groups/groupFormDraft.ts'))
const settings = parseVue('components/admin/group/GroupSettingsForm.vue')

// functionBody returns the AST body of a named function declaration.
function functionBody(root: ts.Node, name: string): ts.Block {
  const fn = nodes(root, ts.isFunctionDeclaration).find(
    (node) => node.name?.text === name && node.body !== undefined,
  )
  if (!fn?.body) throw new Error(`Missing function declaration: ${name}`)
  return fn.body
}

describe('groups scheduler type', () => {
  // Both group modes use the shared scheduler selector and begin with basic scheduling.
  // Editing a group restores its value with a basic default for missing metadata.
  it('connects the shared selector and scheduler defaults in both forms', () => {
    const selector = settings.elements.find(
      (element) =>
        element.tag === 'Select' && directive(element, 'model') === 'form.scheduler_type',
    )!
    expect(directive(selector, 'bind', 'options')).toBe('schedulerOptions')
    expect(
      modal.elements
        .filter((element) => element.tag === 'GroupSettingsForm')
        .map((element) => directive(element, 'bind', 'model-value')),
    ).toEqual(['form'])

    const createDraftBody = functionBody(draftScript, 'createGroupFormDraft')
    const field = nodes(createDraftBody, ts.isPropertyAssignment).find(
      (node) => ts.isIdentifier(node.name) && node.name.text === 'scheduler_type',
    )!
    const value = ts.isAsExpression(field.initializer)
      ? field.initializer.expression
      : field.initializer
    expect(ts.isStringLiteral(value) && value.text).toBe('basic')
    expect(
      nodes(createDraftBody, ts.isPropertyAssignment).some(
        (node) => ts.isIdentifier(node.name) && node.name.text === 'advanced_scheduler_overrides',
      ),
    ).toBe(true)

    const defaults = nodes(
      functionBody(draftScript, 'hydrateGroupFormDraft'),
      ts.isBinaryExpression,
    ).filter(
      (node) =>
        node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken &&
        ts.isPropertyAccessExpression(node.left) &&
        node.left.name.text === 'scheduler_type',
    )
    expect(defaults).toHaveLength(1)
    expect(ts.isStringLiteral(defaults[0].right) && defaults[0].right.text).toBe('basic')
    expect(
      modal.elements.some((element) => element.tag === 'GroupAdvancedSchedulerOverridesModal'),
    ).toBe(true)
    expect(
      [...page.elements, ...modal.elements, ...settings.elements].some(
        (element) => element.tag === 'select',
      ),
    ).toBe(false)
  })
})
