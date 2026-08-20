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

[`docs/setup.md`](docs/setup.md) is the deployment runbook: inference endpoint
requirements, self-hosted runner and egress allowlist, the mandatory `models:`
config block, and a consuming-repo workflow template.

## 📄 License

This repo is licensed under the MIT License. See the [LICENSE](LICENSE.md) file for rights and limitations.
