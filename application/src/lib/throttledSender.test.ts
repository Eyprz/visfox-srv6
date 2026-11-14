import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createThrottledSender } from './throttledSender'

/** A send() whose responses the test resolves or rejects explicitly. */
function controllableSend() {
  const calls: { value: number; resolve: () => void; reject: (e: unknown) => void }[] = []
  const send = vi.fn(
    (value: number) =>
      new Promise<void>((resolve, reject) => calls.push({ value, resolve, reject })),
  )
  return { send, calls }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('createThrottledSender', () => {
  it('sends the first value immediately and then at most every 100 ms with the latest value', async () => {
    const { send, calls } = controllableSend()
    const sender = createThrottledSender(send)
    sender.push(1)
    expect(send).toHaveBeenLastCalledWith(1)
    calls[0].resolve()
    await vi.advanceTimersByTimeAsync(10)

    sender.push(2)
    sender.push(3)
    sender.push(4)
    expect(send).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(90)
    expect(send).toHaveBeenCalledTimes(2)
    expect(send).toHaveBeenLastCalledWith(4)
  })

  it('never has two requests in flight', async () => {
    const { send, calls } = controllableSend()
    const sender = createThrottledSender(send)
    sender.push(1)
    sender.push(2)
    await vi.advanceTimersByTimeAsync(500)
    expect(send).toHaveBeenCalledTimes(1)
    calls[0].resolve()
    await vi.advanceTimersByTimeAsync(0)
    expect(send).toHaveBeenCalledTimes(2)
    expect(send).toHaveBeenLastCalledWith(2)
  })

  it('flush() sends the final value without waiting for the interval', async () => {
    const { send, calls } = controllableSend()
    const sender = createThrottledSender(send)
    sender.push(1)
    calls[0].resolve()
    await vi.advanceTimersByTimeAsync(0)
    sender.push(9)
    const done = sender.flush()
    expect(send).toHaveBeenLastCalledWith(9)
    calls[1].resolve()
    await done
    expect(send).toHaveBeenCalledTimes(2)
  })

  it('drops pending values after a failed send and reports the error', async () => {
    const { send, calls } = controllableSend()
    const onError = vi.fn()
    const sender = createThrottledSender(send, { onError })
    sender.push(1)
    sender.push(2)
    calls[0].reject(new Error('502'))
    await vi.advanceTimersByTimeAsync(200)
    expect(onError).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledTimes(1)
  })
})
