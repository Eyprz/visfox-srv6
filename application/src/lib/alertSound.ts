import type { HealthState } from './types'

// CRITICAL alert sound. Browsers block audio until a user gesture,
// so the header toggle calls unlockAudio() from its click handler.

let ctx: AudioContext | null = null

export function unlockAudio(): void {
  ctx ??= new AudioContext()
  void ctx.resume()
}

/** Two short beeps. No-op until unlockAudio() has been called. */
export function playAlert(): void {
  if (!ctx) return
  const start = ctx.currentTime
  for (const offset of [0, 0.25]) {
    const osc = ctx.createOscillator()
    const gain = ctx.createGain()
    osc.type = 'square'
    osc.frequency.value = 880
    gain.gain.setValueAtTime(0.08, start + offset)
    gain.gain.exponentialRampToValueAtTime(0.0001, start + offset + 0.18)
    osc.connect(gain).connect(ctx.destination)
    osc.start(start + offset)
    osc.stop(start + offset + 0.2)
  }
}

/** Cell ids that became CRITICAL between two health snapshots. */
export function newlyCritical(
  previous: Record<string, HealthState | undefined>,
  next: Record<string, HealthState | undefined>,
): string[] {
  return Object.keys(next).filter((id) => next[id] === 'critical' && previous[id] !== 'critical')
}
