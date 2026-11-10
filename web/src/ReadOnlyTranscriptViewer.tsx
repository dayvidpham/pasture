import type { ComponentProps } from 'react'
import { TranscriptViewer } from '@peasant-labs/fairtrade/ui'

// The console observes transcripts; mutation and export belong to producers.
const capabilities = {
  canEdit: false,
  canLabel: false,
  canContribute: false,
  canChangeVisibility: false,
  canExport: false,
} as const

type ViewerProps = ComponentProps<typeof TranscriptViewer>
type Props = Pick<ViewerProps, 'viewModel' | 'theme' | 'openTools' | 'leftRailOpen' | 'onLeftRailOpenChange' | 'rightRailOpen' | 'onRightRailOpenChange' | 'showTail' | 'presentation'>

export function ReadOnlyTranscriptViewer(props: Props) {
  return <TranscriptViewer {...props} capabilities={capabilities} />
}
