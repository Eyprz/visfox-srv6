import type { VideoFormat } from './types'

// Display labels for API enum values (technical names, identical in every locale).
const videoFormatLabels: Record<VideoFormat, string> = { '8k_dg': '8K DG', '8k_full': '8K FULL' }

export const videoFormatLabel = (format: VideoFormat): string => videoFormatLabels[format] ?? format

export const formatMs = (ms: number, digits = 1): string => `${ms.toFixed(digits)} ms`

/** packet_loss_rate is a 0-1 ratio. */
export const formatLossRate = (rate: number): string => `${(rate * 100).toFixed(3)} %`
