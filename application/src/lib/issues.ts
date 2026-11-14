import { validationIssueText, type Translate } from '../i18n'
import { axisIndexLabel } from './cellId'
import type { BottleneckNode, ValidationIssue } from './types'

/**
 * Operator-facing text of a validation issue: the localized reason, then the bottleneck nodes with the
 * OUTs using them when the IEL names them (resource shortage), else the IEL detail.
 */
export function issueText(t: Translate, issue: ValidationIssue): string {
  // Unknown codes / kinds (added by the IEL later) are shown with the IEL's own text.
  const reason = validationIssueText(t, issue)
  const nodes = issue.bottleneck_nodes ?? []
  if (!reason) return issue.message
  if (nodes.length === 0) return `${reason} ${issue.message}`
  return `${reason} ${bottleneckText(t, nodes)}`
}

/** "Bottleneck: <node> (<OUTs using it>), ..." for the nodes of a VVF that ran out. */
export function bottleneckText(t: Translate, nodes: BottleneckNode[]): string {
  const list = nodes
    .map((n) =>
      n.destination_ids.length > 0
        ? `${n.label} (${n.destination_ids.map(axisIndexLabel).join(', ')})`
        : n.label,
    )
    .join(', ')
  return t('issue.bottleneck', { nodes: list })
}
