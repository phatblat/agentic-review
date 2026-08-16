# agentic-review

`agentic-review` performs variable-sized code review on GitHub pull requests using
locally hosted models on self-hosted runners. Review effort scales with the size and
complexity of the change: trivial changes exit through deterministic classification
with zero model calls, while complex changes assemble a configurable team of reviewer
personas whose findings pass through verification before posting.

## Specification

The authoritative v1 design is in [`docs/spec.md`](docs/spec.md). Read it before
making behavioral changes; this README covers operational setup only.

## Operational setup

Spec §12.1 defines the runner VLAN egress allowlist as "GitHub endpoints (per
`api.github.com/meta`) + the inference endpoint; deny-all otherwise." That list is
incomplete: the `dep-risk` persona's OSV and deps.dev clients (`internal/osv`) need
two more hosts. **Add both to the runner's egress allowlist**, or `dep-risk`
degrades to a single `warning` finding per run rather than failing closed:

- `api.osv.dev` — vulnerability advisory lookups.
- `api.deps.dev` — package release-age lookups.

## 📄 License

This repo is licensed under the MIT License. See the [LICENSE](LICENSE.md) file for rights and limitations.
