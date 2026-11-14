import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp, Maximize2 } from 'lucide-react'
import {
  useEffect,
  useRef,
  useState,
  type PointerEvent,
  type ReactNode,
  type MutableRefObject,
} from 'react'
import { useT } from '../../i18n'
import {
  centerOn,
  clampCrop,
  CROP_REGION,
  moveCrop,
  type CropBounds,
  type CropPoint,
  type Direction,
} from '../../lib/crop'
import { cx } from '../../lib/cx'
import { Modal } from '../common/Modal'

/** Hold speed of the arrow buttons in 8K pixels per second (linear). */
export const HOLD_SPEED = 1200
/** A short click moves this far; holding starts the linear move after HOLD_DELAY_MS. */
export const CLICK_STEP = 8
const HOLD_DELAY_MS = 250
/** Keyboard arrow step (Shift: x10). Key repeat gives the continuous move. */
const KEY_STEP = 20

const keyDirections: Record<string, Direction> = {
  ArrowUp: 'up',
  ArrowDown: 'down',
  ArrowLeft: 'left',
  ArrowRight: 'right',
}

type Latest = { value: CropPoint; onChange: (p: CropPoint) => void; onCommit: () => void }

type Props = {
  value: CropPoint
  bounds: CropBounds
  disabled?: boolean
  /** Every intermediate position (the caller throttles sending to 100 ms). */
  onChange: (p: CropPoint) => void
  /** End of a gesture: the caller sends the final position now. */
  onCommit: () => void
  title?: ReactNode
}

/**
 * Crop position controller, modeled on the arcade controller used in operation:
 * the FHD region is a box inside the 8K frame. Drag the box (or click the frame to center it there),
 * hold the arrow buttons for a linear move, or use the arrow keys on the focused frame. The position is
 * always clamped inside the frame. An enlarge button opens the same controller in a large dialog.
 */
export function CropController(props: Props) {
  const t = useT()
  const [enlarged, setEnlarged] = useState(false)
  return (
    <>
      <ControllerBody {...props} />
      <button
        type="button"
        onClick={() => setEnlarged(true)}
        className="text-accent hover:bg-accent-soft mt-2 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-semibold"
      >
        <Maximize2 className="size-3.5" aria-hidden />
        {t('crop.enlarge')}
      </button>
      <Modal
        open={enlarged}
        onOpenChange={setEnlarged}
        title={props.title ?? t('crop.title')}
        widthClassName="max-w-5xl"
      >
        <ControllerBody {...props} large />
      </Modal>
    </>
  )
}

function ControllerBody({
  value,
  bounds,
  disabled = false,
  onChange,
  onCommit,
  large = false,
}: Props & { large?: boolean }) {
  const t = useT()
  const frameRef = useRef<HTMLDivElement>(null)
  // Latest value / callbacks for the hold loop and pointer handlers, which outlive a render.
  const latest = useRef<Latest>({ value, onChange, onCommit })
  useEffect(() => {
    latest.current = { value, onChange, onCommit }
  })
  const drag = useRef<{ start: CropPoint; px: number; py: number; scale: number } | null>(null)

  const toFrame = (clientX: number, clientY: number) => {
    const r = frameRef.current!.getBoundingClientRect()
    return {
      x: ((clientX - r.left) / r.width) * bounds.frameW,
      y: ((clientY - r.top) / r.height) * bounds.frameH,
      scale: bounds.frameW / r.width,
    }
  }

  const onFramePointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (disabled || e.button !== 0) return
    e.currentTarget.focus()
    e.currentTarget.setPointerCapture(e.pointerId)
    const p = toFrame(e.clientX, e.clientY)
    const onBox = (e.target as HTMLElement).dataset.cropBox !== undefined
    // A press outside the box centers it there first, then the drag continues from that position.
    const start = onBox ? value : centerOn(p, bounds)
    if (!onBox) onChange(start)
    drag.current = { start, px: e.clientX, py: e.clientY, scale: p.scale }
  }
  const onFramePointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const d = drag.current
    if (!d) return
    onChange(
      clampCrop(
        {
          x: d.start.x + (e.clientX - d.px) * d.scale,
          y: d.start.y + (e.clientY - d.py) * d.scale,
        },
        bounds,
      ),
    )
  }
  const endDrag = () => {
    if (!drag.current) return
    drag.current = null
    onCommit()
  }

  const pct = (v: number, of: number) => `${(v / of) * 100}%`

  return (
    <div className={cx('flex gap-3', large ? 'items-center' : 'flex-col')}>
      <div
        ref={frameRef}
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-label={t('crop.frame')}
        aria-valuetext={`x ${value.x}, y ${value.y}`}
        aria-disabled={disabled || undefined}
        onPointerDown={onFramePointerDown}
        onPointerMove={onFramePointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        onKeyDown={(e) => {
          const dir = keyDirections[e.key]
          if (!dir || disabled) return
          e.preventDefault()
          onChange(moveCrop(value, dir, e.shiftKey ? KEY_STEP * 10 : KEY_STEP, bounds))
        }}
        onKeyUp={(e) => keyDirections[e.key] && !disabled && onCommit()}
        style={{ aspectRatio: `${bounds.frameW} / ${bounds.frameH}` }}
        className={cx(
          'bg-selected focus-visible:outline-accent relative w-full touch-none overflow-hidden rounded select-none focus-visible:outline-3 focus-visible:outline-offset-2',
          large && 'flex-1',
          disabled ? 'cursor-not-allowed opacity-50' : 'cursor-crosshair',
        )}
      >
        <span className="text-on-selected/60 pointer-events-none absolute right-2 bottom-1 text-[0.625rem] font-semibold">
          8K {bounds.frameW} x {bounds.frameH}
        </span>
        <div
          data-crop-box
          className={cx(
            'border-accent bg-accent/30 absolute flex items-center justify-center rounded-sm border-2 text-[0.625rem] font-bold text-white',
            !disabled && 'cursor-move',
          )}
          style={{
            left: pct(value.x, bounds.frameW),
            top: pct(value.y, bounds.frameH),
            width: pct(CROP_REGION.w, bounds.frameW),
            height: pct(CROP_REGION.h, bounds.frameH),
          }}
        >
          <span className="pointer-events-none">FHD</span>
        </div>
      </div>
      <div className={cx('flex items-center gap-4', large && 'flex-col')}>
        <ArrowPad disabled={disabled} bounds={bounds} latest={latest} large={large} />
        <dl className="grid grid-cols-[auto_auto] gap-x-2 text-sm tabular-nums">
          <dt className="text-ink-subtle">x</dt>
          <dd className="text-right font-semibold">{value.x}</dd>
          <dt className="text-ink-subtle">y</dt>
          <dd className="text-right font-semibold">{value.y}</dd>
        </dl>
      </div>
    </div>
  )
}

/**
 * Up / down / left / right buttons. A click moves CLICK_STEP; holding moves linearly at HOLD_SPEED
 * (requestAnimationFrame, so the speed does not depend on the frame rate). Release commits.
 */
function ArrowPad({
  disabled,
  bounds,
  latest,
  large,
}: {
  disabled: boolean
  bounds: CropBounds
  latest: MutableRefObject<Latest>
  large: boolean
}) {
  const t = useT()
  const hold = useRef<{ timer?: ReturnType<typeof setTimeout>; frame?: number } | null>(null)

  const stop = (commit = true) => {
    const h = hold.current
    if (!h) return
    clearTimeout(h.timer)
    if (h.frame) cancelAnimationFrame(h.frame)
    hold.current = null
    if (commit) latest.current.onCommit()
  }

  // Stop a hold if the controller unmounts mid-press (no commit: the editor flushes on unmount).
  useEffect(
    () => () => {
      const h = hold.current
      if (!h) return
      clearTimeout(h.timer)
      if (h.frame) cancelAnimationFrame(h.frame)
    },
    [],
  )

  const start = (dir: Direction) => {
    if (disabled) return
    stop(false)
    const { value, onChange } = latest.current
    onChange(moveCrop(value, dir, CLICK_STEP, bounds))
    const h: { timer?: ReturnType<typeof setTimeout>; frame?: number } = {}
    hold.current = h
    h.timer = setTimeout(() => {
      let last = performance.now()
      const step = (now: number) => {
        const cur = latest.current
        cur.onChange(moveCrop(cur.value, dir, (HOLD_SPEED * (now - last)) / 1000, bounds))
        last = now
        h.frame = requestAnimationFrame(step)
      }
      h.frame = requestAnimationFrame(step)
    }, HOLD_DELAY_MS)
  }

  const button = (dir: Direction, icon: ReactNode, area: string) => (
    <button
      type="button"
      disabled={disabled}
      aria-label={t(`crop.${dir}`)}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId)
        start(dir)
      }}
      onPointerUp={() => stop()}
      onPointerCancel={() => stop()}
      onContextMenu={(e) => e.preventDefault()}
      // Keyboard activation of the button: one step, then commit.
      onKeyDown={(e) => {
        if (e.key !== 'Enter' && e.key !== ' ') return
        e.preventDefault()
        const { value, onChange } = latest.current
        onChange(moveCrop(value, dir, CLICK_STEP, bounds))
      }}
      onKeyUp={(e) => (e.key === 'Enter' || e.key === ' ') && latest.current.onCommit()}
      style={{ gridArea: area }}
      className={cx(
        'border-line-strong bg-surface hover:bg-surface-muted flex touch-none items-center justify-center rounded border select-none disabled:opacity-50',
        large ? 'size-12' : 'size-9',
      )}
    >
      {icon}
    </button>
  )

  return (
    <div className="grid gap-1" style={{ gridTemplateAreas: '". u ." "l . r" ". d ."' }}>
      {button('up', <ChevronUp className="size-5" aria-hidden />, 'u')}
      {button('left', <ChevronLeft className="size-5" aria-hidden />, 'l')}
      {button('right', <ChevronRight className="size-5" aria-hidden />, 'r')}
      {button('down', <ChevronDown className="size-5" aria-hidden />, 'd')}
    </div>
  )
}
