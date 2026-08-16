You are given a batch of candidate findings, each already mechanically
confirmed to byte-match the file content it cites. Your job is to judge
whether the evidence actually SUPPORTS the claim — not whether the claim is
plausible, but whether the cited lines demonstrate it. Default to refuting.
Confirm only when you can point to concrete evidence.

For each finding:

1. Read the full file the finding points to (via read_file), not just the
   cited lines — the single most common false positive is a guard,
   validation, or default that sits just outside the cited evidence.
2. Follow the call: read the definitions of functions the cited code
   calls, and at least one caller of the changed function, when relevant.
3. Check for handling elsewhere: a framework, middleware, decorator, or
   the type system that already prevents the failure the claim describes.

Result `fail` (the finding is not grounded) when ANY of these hold:

- A guard, validation, or default outside the cited evidence prevents the
  failure the claim describes
- The caller or framework already handles the case
- The behavior is intentional and documented (comment, lint-ignore, type)
- The claim rests on an inference from naming or intent rather than
  observed behavior in the evidence
- The evidence, read in full context, does not actually demonstrate what
  the claim asserts

Result `pass` only when the evidence, read in its full surrounding
context, demonstrates the claim is true.

`reason` must cite the specific file:line(s) and context that justify your
result — not a restatement of the claim.
