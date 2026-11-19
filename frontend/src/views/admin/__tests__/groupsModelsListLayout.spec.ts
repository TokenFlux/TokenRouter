import { describe, expect, it } from 'vitest'
import ts from 'typescript'
import {
  attribute,
  classes,
  directive,
  nodes,
  parseScript,
  parseVue,
} from '@/__tests__/helpers/sourceSyntax'

// groupsModelsListLayout.spec.ts verifies the model chooser's template hierarchy
// and dialog scrolling policy. Toolbar actions stay outside the bounded list,
// while the shared group form modal delegates scrolling to its form content and
// presents the unified model discovery endpoint.

// Parsed components retain element relationships and directive expressions;
// assertions therefore identify the target control instead of matching source lines.
const groups = parseVue('views/admin/GroupsView.vue')
const modal = parseVue('views/admin/groups/GroupFormModal.vue')
const list = parseVue('components/admin/group/GroupModelsListFields.vue')

describe('groups models list layout', () => {
  // A long model list scrolls below the selection toolbar inside one bordered panel.
  // Keeping the toolbar as a sibling makes its actions available during list scrolling.
  it('keeps the toolbar outside of the scrolling list content', () => {
    const panel = list.elements.find((element) => classes(element).includes('overflow-hidden'))!
    expect(classes(panel)).toEqual(expect.arrayContaining(['rounded-surface', 'border']))
    const children = panel.children.filter((child) => child.type === 1)
    expect(children).toHaveLength(2)
    expect(classes(children[0])).toEqual(expect.arrayContaining(['border-b']))
    expect(classes(children[1])).toEqual(
      expect.arrayContaining(['max-h-64', 'divide-y', 'divide-gray-200', 'overflow-y-auto']),
    )
    expect(
      [...groups.elements, ...modal.elements].some(
        (element) => classes(element).includes('sticky') && classes(element).includes('top-0'),
      ),
    ).toBe(false)
  })

  // GroupFormModal serves both create and edit modes inside one wide BaseDialog
  // with form-owned scrolling, preventing a nested dialog scrollbar.
  it('uses wide create and edit dialogs with form-owned scrolling', () => {
    const dialogs = modal.elements.filter(
      (element) => element.tag === 'BaseDialog' && attribute(element, 'width') === 'wide',
    )
    expect(dialogs).toHaveLength(1)
    expect(directive(dialogs[0], 'bind', 'body-scroll')).toBe('false')
    expect(groups.elements.some((element) => element.tag === 'GroupFormModal')).toBe(true)
  })

  // The model-list label and hint advertise the same API endpoint for every group.
  // Their parsed translation arguments retain the endpoint value across quote and layout changes.
  it('uses the unified models endpoint in group configuration', () => {
    const toggle = list.elements.find((element) => element.tag === 'SettingToggleRow')!
    for (const property of ['label', 'hint']) {
      const expression = parseScript(directive(toggle, 'bind', property)!)
      const endpoint = nodes(expression, ts.isPropertyAssignment).find(
        (node) => ts.isIdentifier(node.name) && node.name.text === 'endpoint',
      )!
      expect(ts.isStringLiteral(endpoint.initializer) && endpoint.initializer.text).toBe(
        '/v1/models',
      )
    }
    expect(
      [
        ...nodes(groups.script, ts.isCallExpression),
        ...nodes(modal.script, ts.isCallExpression),
      ].some(
        (node) => ts.isIdentifier(node.expression) && node.expression.text === 'modelsListEndpoint',
      ),
    ).toBe(false)
  })
})
