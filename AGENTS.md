# Agent Instructions

## Pull Requests

- Always create PRs as ready for review (do not use `--draft`).

## Pushing Code

- Before pushing, always fetch and rebase onto the latest remote base branch
  (`git fetch origin && git rebase origin/main`) to avoid conflicts caused by
  divergent histories. This applies to both regular pushes and force pushes.
