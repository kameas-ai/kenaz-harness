# CLA and OSS attributions — how this repo's automation works

This repo carries two GitHub Actions workflows that enforce the legal
hygiene around third-party contributions and dependencies:

| Workflow | Purpose | Triggered by |
|---|---|---|
| `.github/workflows/cla.yml` | Block merges from contributors who haven't signed the [Kameas CLA](https://kameas.ai/cla.html). | Every PR open / sync; PR comments matching the sign phrase. |
| `.github/workflows/oss-attributions.yml` | Regenerate the `NOTICES` file from `go.mod` and `frontend/package.json`; open a PR if the file changed. | `release: published`; tag push matching `v*`; manual dispatch. |

Both workflows were adopted from the org-level templates at
[`kameas-ai/.github`](https://github.com/kameas-ai/.github/tree/main/workflow-templates).
Adopting them in another Kameas OSS repo is one click from the GitHub
Actions "New workflow" UI under the "By Kameas AI" section.

## One-time setup for this repo

Both workflows authenticate via the **`kameas-ai-oss-bot` GitHub App**
(the same kind of org-installed App used by `kameas-ai-deploy` for the
fleet deploy workflows). One App, two repo secrets per adopting repo:

1. **Confirm the App is installed on this repo.** Visit
   <https://github.com/organizations/kameas-ai/settings/installations>,
   click `kameas-ai-oss-bot` → **Configure**, and tick this repo (plus
   `cla-signatures` for the CLA workflow's cross-repo writes).

2. **Add the two repo secrets:**

   ```bash
   gh secret set KAMEAS_OSS_BOT_APP_ID -R kameas-ai/<this-repo>
   # paste the numeric App ID, Enter, Ctrl-D

   cat path/to/kameas-ai-oss-bot.private-key.pem \
     | gh secret set KAMEAS_OSS_BOT_APP_PRIVATE_KEY -R kameas-ai/<this-repo>
   ```

   (`gh secret set` reads from stdin when no `-b` / `--body` flag is
   given. The `--body-file` flag is documented in some places but is
   not actually implemented in the current `gh` CLI; piping from `cat`
   or from `aws secretsmanager get-secret-value` is the working idiom.)

   Both values are also stored in AWS Secrets Manager (us-east-2) under
   `kameas-ai/oss-bot/github-app-id` and
   `kameas-ai/oss-bot/github-app-private-key` as the authoritative
   copies. If you need to re-bootstrap a repo's secrets:

   ```bash
   aws secretsmanager get-secret-value --region us-east-2 \
     --secret-id kameas-ai/oss-bot/github-app-id \
     --query SecretString --output text \
     | gh secret set KAMEAS_OSS_BOT_APP_ID -R kameas-ai/<this-repo>

   aws secretsmanager get-secret-value --region us-east-2 \
     --secret-id kameas-ai/oss-bot/github-app-private-key \
     --query SecretString --output text \
     | gh secret set KAMEAS_OSS_BOT_APP_PRIVATE_KEY -R kameas-ai/<this-repo>
   ```

3. **No PAT required.** Earlier versions of these workflows used a
   `PERSONAL_ACCESS_TOKEN` repo secret; that secret can be removed once
   the App-based workflows have run successfully at least once. App
   installation tokens are minted at workflow runtime via
   `actions/create-github-app-token@v1`, expire in 1 hour, and never
   exist as a long-lived secret.

## Day-to-day behavior

### CLA workflow

- A first-time outside contributor opens a PR. The bot comments with a
  signing link and applies the `cla-not-signed` label. The PR is blocked
  from merging.
- The contributor signs by replying to the PR with the literal phrase
  `I have read the CLA Document and I hereby sign the CLA`. The bot
  appends them to `signatures/v1/cla.json` in
  [`kameas-ai/cla-signatures`](https://github.com/kameas-ai/cla-signatures),
  removes the label, and adds a confirming comment.
- Any future PR by the same contributor (in this repo or any other
  Kameas OSS repo that uses the shared signatures store) auto-passes.
- Allowlisted accounts (`dependabot[bot]`, `renovate[bot]`,
  `github-actions[bot]`, `alecfeeman` — the org founder) skip the check.
  To add more org members or bots, edit the `allowlist:` line in
  `.github/workflows/cla.yml`.

### OSS attributions workflow

- When a release is published (or a `v*` tag is pushed, or someone runs
  it manually), the workflow:
  1. Detects which manifests are present (`go.mod`, `frontend/package.json`).
  2. Runs `go-licenses report ./...` for Go modules.
  3. Runs `license-checker --production --json` in `frontend/`.
  4. Combines both into a fresh `NOTICES` using the template at
     `scripts/oss/go-template.tpl` and the Python formatter at
     `scripts/oss/format-node-notices.py`.
  5. Preserves any manually-curated trailing section below an
     `## Asset bundles` heading (so non-code attributions like fonts and
     vendored design tokens stay in place).
  6. If `NOTICES` changed, opens a PR titled
     `chore(notices): regenerate OSS attributions` for a maintainer to
     review and merge.
- The PR carries the `oss-attributions` and `chore` labels.
- Review focus: any new dependency under a license that adds obligations
  beyond attribution (LGPL, MPL, EPL — source-disclosure). Anything
  unusual should be routed to <legal@kameas.ai> before merging.

## Updating the templates

The org-level templates at `kameas-ai/.github/workflow-templates/` are the
source of truth for new adoptions. Each repo carries its own copy of the
workflow (GitHub doesn't auto-sync template updates into adopting repos),
so to roll a change across repos:

1. Update the template in `kameas-ai/.github`.
2. For each adopting repo, open a PR replacing the copy in
   `.github/workflows/` with the new template. A small `gh`-based script
   makes this easy.

## Withdrawing a signature

Per CLA §9, a contributor may withdraw acceptance for future contributions
by emailing <legal@kameas.ai>. A maintainer removes their entry from
`signatures/v1/cla.json` in `kameas-ai/cla-signatures` by direct commit.
Contributions already submitted are unaffected (the CLA grants are
irrevocable for already-submitted work).

## Mapping to legal documents

- **CLA text:** <https://kameas.ai/cla.html>
- **Trademark policy:** <https://kameas.ai/trademarks.html>
- **Public attributions roll-up:** <https://kameas.ai/oss-attributions.html>
  (auto-generated from per-repo `NOTICES` files as part of a future
  marketing-site build step).
