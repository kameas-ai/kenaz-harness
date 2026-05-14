# Runbook: bake Wails toolchain into the kameas-ci-medium AMI

**Status**: draft · **Owner**: ops · **Scope**: kameas-ai AWS account

## 1. Why

`.github/workflows/release.yml` builds `linux-arm64` binaries on `ubuntu-24.04-arm` (GitHub-hosted) instead of the self-hosted `kameas-ci-medium` pool because the current runner image lacks the Wails build dependencies:

- `libgtk-3-dev`
- `libwebkit2gtk-4.1-dev`
- `pkg-config`

This is documented drift in `CLAUDE.md` ("Self-hosted runner caveats") and in `docs/roadmap.md` under the v0.13.x patch lane. Moving the build back to self-hosted reduces GitHub-billing minutes and aligns with org policy that GitHub-hosted runners be reserved for Apple/Microsoft platform builds.

This runbook bakes a new AMI revision and rotates the runner pool onto it.

## 2. Prerequisites

- AWS CLI configured against the **kameas-ai** account, with permissions to:
  - `ec2:RunInstances`, `ec2:CreateImage`, `ec2:DeregisterImage`, `ec2:CreateTags`
  - the role / instance profile that the existing runners use (mirrored to the new image)
- Packer ≥ 1.10 installed locally (`packer version`)
- SSH key pair already imported to the region (default region: `us-east-1` per existing runner config)
- Read access to the current AMI ID — capture it with:
  ```bash
  aws ec2 describe-images --owners self \
    --filters "Name=tag:Name,Values=kameas-ci-medium" \
    --query 'Images[?State==`available`] | sort_by(@, &CreationDate) | [-1].[ImageId,Name,CreationDate]' \
    --output table
  ```

## 3. Packer template

Save as `kameas-ci-medium.pkr.hcl` in your local working directory (this file is **not** committed to the application repo — keep it in a separate `kameas-ci-infra` repo or your ops-runbooks repo).

```hcl
packer {
  required_plugins {
    amazon = {
      version = ">= 1.3.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "source_ami" {
  type        = string
  description = "AMI ID of the current kameas-ci-medium image (the base to extend)"
}

variable "image_revision" {
  type        = string
  description = "Revision label, e.g. 'wails-toolchain-2026-05'"
}

source "amazon-ebs" "kameas_ci_medium" {
  region        = var.region
  source_ami    = var.source_ami
  instance_type = "m6g.medium"          # ARM64 — matches ci-medium pool

  ssh_username = "ubuntu"
  ssh_timeout  = "10m"

  ami_name        = "kameas-ci-medium-${var.image_revision}"
  ami_description = "kameas-ci-medium with Wails build toolchain (libgtk-3-dev, libwebkit2gtk-4.1-dev, pkg-config) — baked ${formatdate("YYYY-MM-DD", timestamp())}"

  tags = {
    Name        = "kameas-ci-medium"
    Revision    = var.image_revision
    BakedAt     = timestamp()
    Toolchain   = "wails"
    SourceAMI   = var.source_ami
  }
}

build {
  sources = ["source.amazon-ebs.kameas_ci_medium"]

  # Verify we're on a Debian-family image. The existing kameas-ci AMI is
  # documented (CLAUDE.md) as NOT Debian-family — if that's still true
  # this build will fail and a different package manager must be used.
  # The provisioner below is a hard assertion at bake time.
  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "if ! command -v apt-get >/dev/null 2>&1; then",
      "  echo 'ERROR: this image is not Debian-family. Adapt the provisioner to the actual package manager (rpm/dnf/apk/etc).' >&2",
      "  exit 1",
      "fi",
      "echo 'Confirmed Debian-family base.'",
    ]
  }

  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "export DEBIAN_FRONTEND=noninteractive",
      "sudo apt-get update -qq",
      "sudo apt-get install -y --no-install-recommends \\",
      "    libgtk-3-dev \\",
      "    libwebkit2gtk-4.1-dev \\",
      "    pkg-config",
      "# Verify pkg-config can resolve both libraries — required by the",
      "# Wails build (release.yml: wails build -platform=linux/arm64).",
      "pkg-config --exists gtk+-3.0 && echo 'gtk+-3.0 OK'",
      "pkg-config --exists webkit2gtk-4.1 && echo 'webkit2gtk-4.1 OK'",
      "sudo apt-get clean",
      "sudo rm -rf /var/lib/apt/lists/*",
    ]
  }
}
```

## 4. Bake the AMI

```bash
# Pin the source AMI ID from the prerequisites step:
export SOURCE_AMI=ami-0123456789abcdef0    # ← replace
export REVISION="wails-toolchain-$(date +%Y-%m)"

packer init kameas-ci-medium.pkr.hcl
packer validate \
  -var "source_ami=$SOURCE_AMI" \
  -var "image_revision=$REVISION" \
  kameas-ci-medium.pkr.hcl

packer build \
  -var "source_ami=$SOURCE_AMI" \
  -var "image_revision=$REVISION" \
  kameas-ci-medium.pkr.hcl
```

Expected output ends with `==> Builds finished. The artifacts of successful builds are:` and an `ami-…` ID for the new image. Capture it as `NEW_AMI`.

## 5. Verify the AMI on a throwaway instance

```bash
aws ec2 run-instances \
  --image-id "$NEW_AMI" \
  --instance-type m6g.medium \
  --key-name your-key \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=kameas-ci-ami-smoke}]' \
  --query 'Instances[0].InstanceId' --output text
```

SSH in, run:

```bash
pkg-config --modversion gtk+-3.0       # → 3.x.x
pkg-config --modversion webkit2gtk-4.1 # → 2.x.x
# Quick Wails smoke (optional; needs Go + Wails installed):
# wails doctor
```

Terminate the instance when done.

## 6. Rotate the runner pool onto the new AMI

The kameas-ci pool's launch template references the AMI. Update the default version:

```bash
# Find the launch template:
aws ec2 describe-launch-templates \
  --filters "Name=tag:Name,Values=kameas-ci-medium*" \
  --query 'LaunchTemplates[].[LaunchTemplateId,LaunchTemplateName]' --output table

# Create a new version pointing at the new AMI:
export LT_ID=lt-0123456789abcdef0    # ← from previous command
aws ec2 create-launch-template-version \
  --launch-template-id "$LT_ID" \
  --source-version '$Latest' \
  --launch-template-data "{\"ImageId\":\"$NEW_AMI\"}"

# Mark the new version as default:
aws ec2 modify-launch-template \
  --launch-template-id "$LT_ID" \
  --default-version '$Latest'
```

For Auto Scaling Group-managed pools, follow up with an instance refresh:

```bash
export ASG_NAME=kameas-ci-medium-asg    # ← actual ASG name
aws autoscaling start-instance-refresh \
  --auto-scaling-group-name "$ASG_NAME" \
  --strategy Rolling \
  --preferences '{"MinHealthyPercentage":50,"InstanceWarmup":120}'
```

For runner-controller-managed pools (e.g. `actions/actions-runner-controller`), bump the image reference in the runner CRD and apply. Specifics depend on the controller setup — not in scope of this runbook.

## 7. Flip `release.yml` back to self-hosted for linux-arm64

Once at least one new-image runner is online and healthy, update `.github/workflows/release.yml`:

```diff
   build-linux-arm64:
-    runs-on: ubuntu-24.04-arm
+    runs-on: [self-hosted, Linux, ARM64, ci-medium]
     steps:
       - uses: actions/checkout@v4
       ...
```

Use the same `runs-on` token shape as the other self-hosted jobs in the file (see `wire-golden-locked` in `pr.yml` for the canonical pattern). Ship the change as a `ci(release):` patch — it triggers `pr-title.yml` validation but bumps neither tag nor release (per `CLAUDE.md` patch-prefix convention, `ci:` is no-bump).

## 8. Rollback

If the new image causes failures, revert by pointing the launch template back at the previous AMI version:

```bash
aws ec2 modify-launch-template \
  --launch-template-id "$LT_ID" \
  --default-version '<previous version number>'

aws autoscaling start-instance-refresh \
  --auto-scaling-group-name "$ASG_NAME" \
  --strategy Rolling
```

Or revert `release.yml`'s `runs-on` back to `ubuntu-24.04-arm` and the linux-arm64 builds resume on GitHub-hosted (documented drift, costs minutes).

## 9. Cleanup

Deregister the old AMI after ≥1 week of soak:

```bash
aws ec2 deregister-image --image-id "$SOURCE_AMI"
# Find and delete the orphaned snapshot:
aws ec2 describe-snapshots --owner-ids self \
  --filters "Name=description,Values=*$SOURCE_AMI*" \
  --query 'Snapshots[].SnapshotId' --output text
# aws ec2 delete-snapshot --snapshot-id snap-…
```

## 10. Open questions

- **Is the current runner image actually Debian-family?** `CLAUDE.md` says it is **not** (no `apt-get`). The Packer template's first provisioner step asserts at bake time and fails fast if not. If the image is RPM-family (Amazon Linux 2023 / Rocky / etc), swap the install commands to `dnf install -y gtk3-devel webkit2gtk4.1-devel pkg-config`.
- **Is the launch template managed by Terraform or hand-rolled?** If Terraform, edit the IaC and let `terraform apply` perform the version bump instead of the imperative steps in §6.
- **Multi-region**: this runbook bakes in `us-east-1` only. If the runner pool spans regions, repeat steps 4–6 per region (Packer `region` variable).
