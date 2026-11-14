// Cell id convention from api/openapi.yaml (CellId): source `in-{n}` x destination `out-{m}` -> `IN{n}-OUT{m}`.

export function cellIdOf(sourceId: string, destinationId: string): string {
  return `IN${sourceId.replace(/^in-/, '')}-OUT${destinationId.replace(/^out-/, '')}`
}

/** Short axis label from the resource id: `in-3` -> `IN 3`, `out-12` -> `OUT 12`. */
export function axisIndexLabel(resourceId: string): string {
  const m = /^(in|out)-(.+)$/.exec(resourceId)
  return m ? `${m[1].toUpperCase()} ${m[2]}` : resourceId
}
