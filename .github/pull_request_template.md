## Summary

<!-- What changes and why. Link the issue: "Closes #123". -->

## Type

- [ ] feat: new feature
- [ ] fix: bug fix
- [ ] docs: documentation
- [ ] refactor / test / chore

## Checklist

- [ ] The title follows Conventional Commits, `type(scope): message` (see CONTRIBUTING.md)
- [ ] `api/openapi.yaml` changed first if the API changes; lint and code generation rerun
- [ ] IEL: `go test -race ./...` and `golangci-lint run` pass
- [ ] Application Layer: `npm run check`, `npm run lint` and `npm run test` pass
- [ ] The WebUI stays declarative: no SIDs, Segment Lists or chain rules computed in UI code
- [ ] Documentation updated where behavior or configuration changed

## How it was tested

<!-- Mock IEL, Go IEL dry run, C-Plane lab, or a real network; what you checked. -->
