// @vitest-environment happy-dom
import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { TranscriptViewer } from '@peasant-labs/fairtrade/ui'
import { expect, it, vi } from 'vitest'

import { ReadOnlyTranscriptViewer } from './ReadOnlyTranscriptViewer'
import { adaptTranscriptPayload } from './transcript'
import syntheticTranscript from './testdata/synthetic-transcript.json'

it('gates interactive transcript actions against an enabled viewer', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  const viewModel = adaptTranscriptPayload(syntheticTranscript)

  const button = (text: string) => [...container.querySelectorAll('button')]
    .find((element) => element.textContent?.trim() === text)
  const menuItems = (name: string) => [...container.querySelectorAll(`[role="menu"][aria-label="${name}"] [role="menuitem"]`)]
    .map((element) => element.textContent?.trim())
  const click = async (element: HTMLButtonElement | undefined) => {
    expect(element).toBeDefined()
    await act(async () => { element!.click() })
  }

  try {
    // Upstream defaults to hidden tail and ledger presentation; enable the
    // actual tail and classic presentation so every capability can surface.
    await act(async () => { root.render(createElement(ReadOnlyTranscriptViewer, { viewModel, showTail: true, presentation: 'classic' })) })
    expect(container.textContent).toContain('Synthetic user')
    expect(container.querySelector('button[aria-label="more actions"]')).toBeNull()
    expect(container.querySelector('.txn-labelbtn')).toBeNull()
    await click(button('share'))
    expect(menuItems('share')).toEqual(['copy link'])

    // The actual upstream viewer with every capability enabled is a positive
    // control for the same selectors, fixture, client renderer and interactions.
    await act(async () => { root.render(createElement(TranscriptViewer, {
      viewModel,
      showTail: true,
      presentation: 'classic',
      capabilities: { canEdit: true, canExport: true, canContribute: true, canChangeVisibility: true, canLabel: true },
    })) })
    await click(button('share'))
    expect(menuItems('share')).toEqual(['contribute', 'copy link', 'visibility'])
    expect(button('contribute')?.disabled).toBe(false)
    expect(button('visibility')?.disabled).toBe(false)
    await click(container.querySelector<HTMLButtonElement>('button[aria-label="more actions"]') ?? undefined)
    expect(menuItems('more actions')).toEqual(['edit', 'json', 'jsonl', 'markdown'])
    for (const name of ['edit', 'json', 'jsonl', 'markdown']) expect(button(name)?.disabled).toBe(false)
    const label = container.querySelector<HTMLButtonElement>('.txn-labelbtn')
    expect(label?.disabled).toBe(false)
    await click(label ?? undefined)
    expect(button('save label')?.disabled).toBe(false)
  } finally {
    await act(async () => { root.unmount() })
    container.remove()
    vi.unstubAllGlobals()
  }
})
