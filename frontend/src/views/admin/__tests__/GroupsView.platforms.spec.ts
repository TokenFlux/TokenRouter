import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import {
  nodes,
  parseScript,
  parseVue,
  readSource,
  variableInitializer,
} from '@/__tests__/helpers/sourceSyntax'

// GroupsView.platforms.spec.ts checks the fields represented by general group
// drafts and their submit payloads. Groups apply across provider platforms, so
// shared create and edit state omit platform selection and the retired
// default-group flag while preserving request construction.

// Parsed AST trees from the group page, modal, and shared draft module identify
// draft fields and submitted object properties directly.
const pageScript = parseVue('views/admin/GroupsView.vue').script
const modalScript = parseVue('views/admin/groups/GroupFormModal.vue').script
const draftScript = parseScript(readSource('views/admin/groups/groupFormDraft.ts'))

// functionBody returns the AST body of a named function declaration.
function functionBody(root: ts.Node, name: string): ts.Block {
  const fn = nodes(root, ts.isFunctionDeclaration).find(
    (node) => node.name?.text === name && node.body !== undefined,
  )
  if (!fn?.body) throw new Error(`Missing function declaration: ${name}`)
  return fn.body
}

describe('general group management', () => {
  // Creating or editing a general group must not submit platform or default-group fields.
  // Shared drafts and request builders are inspected because payloads spread the draft.
  it('omits platform and default-group fields from drafts and payloads', () => {
    const createDraftFn = functionBody(draftScript, 'createGroupFormDraft')
    const draftReturn = nodes(createDraftFn, ts.isReturnStatement)[0]?.expression
    expect(draftReturn && ts.isObjectLiteralExpression(draftReturn)).toBe(true)

    const objects = [
      draftReturn!,
      variableInitializer(functionBody(draftScript, 'buildCreateGroupRequest'), 'requestData'),
      variableInitializer(functionBody(draftScript, 'buildUpdateGroupRequest'), 'payload'),
    ]
    for (const object of objects) {
      const names = nodes(object, ts.isPropertyAssignment).map((node) =>
        ts.isIdentifier(node.name) || ts.isStringLiteral(node.name) ? node.name.text : '',
      )
      expect(names).not.toContain('platform')
      expect(names).not.toContain('is_default')
    }
    for (const script of [pageScript, modalScript, draftScript]) {
      expect(
        nodes(script, ts.isPropertyAccessExpression).some(
          (node) =>
            ts.isIdentifier(node.expression) &&
            node.expression.text === 'form' &&
            node.name.text === 'platform',
        ),
      ).toBe(false)
    }
  })
})
