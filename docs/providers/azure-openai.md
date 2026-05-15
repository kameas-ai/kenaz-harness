# Azure OpenAI Provider

This guide covers configuring the Azure OpenAI Service adapter in the kenaz-harness.

## Overview

The `azure-openai` adapter speaks the Azure OpenAI Service REST API, which is
structurally identical to the OpenAI Chat Completions API but:

- Routes through a **tenant-specific resource hostname** (not `api.openai.com`).
- Uses **deployment IDs** in the URL path rather than model names.
- Authenticates with an `api-key` header (not `Authorization: Bearer`).
- Requires an **api-version** query parameter on every request.

Capability surface: Azure OpenAI mirrors OpenAI's capability catalog. The
harness queries the OpenAI YAML descriptors for Azure models automatically —
you do not need a separate `azure-openai.yaml`.

## Prerequisites

1. An Azure subscription with an Azure OpenAI resource provisioned.
2. At least one **deployment** created in Azure AI Studio for your resource.
3. An **API key** from the resource's "Keys and Endpoint" page in the Azure portal.

## Configuring Deployments

The adapter resolves `(resourceHost, modelID)` pairs to Azure deployment names
and api-versions via a `deployments.yaml` file. Create this file at a path of
your choice and point the harness at it:

```bash
export HARNESS_AZURE_DEPLOYMENTS_PATH=/etc/harness/azure-deployments.yaml
```

### Example `deployments.yaml`

```yaml
version: 1
deployments:
  - host: myresource.openai.azure.com
    model_id: gpt-4o
    deployment: prod-gpt-4o-eastus
    api_version: "2024-10-21"

  - host: myresource.openai.azure.com
    model_id: o1
    deployment: prod-o1-eastus
    api_version: "2025-01-01-preview"

  - host: myresource.openai.azure.com
    model_id: gpt-4-turbo
    deployment: gpt-4-turbo-v1
    api_version: "2024-10-21"
```

**Fields:**

| Field | Description |
|---|---|
| `host` | Azure OpenAI resource hostname WITHOUT `https://`. Find it in the Azure portal under "Keys and Endpoint". |
| `model_id` | The canonical OpenAI model name used in GenerationRequest.Model (e.g. `gpt-4o`, `o1`). |
| `deployment` | The Azure deployment name you created in Azure AI Studio. |
| `api_version` | The Azure OpenAI api-version string. Use `"2024-10-21"` for GA models. |

### Where to find the resource name in the Azure portal

1. Open the Azure portal → **Azure OpenAI** → select your resource.
2. Go to **Keys and Endpoint** (left sidebar).
3. Copy the **Endpoint** URL — the hostname is everything between `https://` and `/` (e.g. `myresource.openai.azure.com`).

### api-version recommendations

| Model family | Recommended api-version |
|---|---|
| `gpt-4o`, `gpt-4-turbo`, `gpt-3.5-turbo` | `2024-10-21` (GA) |
| `o1`, `o3` (preview) | `2025-01-01-preview` |
| `gpt-4o-mini` | `2024-10-21` |

## Adding a Provider via the UI

1. Open the harness **Providers** tab.
2. Select **Azure OpenAI** from the provider kind dropdown.
3. Enter the **Resource Hostname** (e.g. `myresource.openai.azure.com`).
4. Paste your **API Key** from the Azure portal.
5. Enter the model IDs you want to authorise — one per line (e.g. `gpt-4o`, `o1`).
6. Click **Submit**.

The API key is written to your OS keychain and never stored in `providers.json`.

## Feature Flag

The Azure OpenAI adapter is enabled by default. To disable it:

```bash
export HARNESS_AZURE_OPENAI=0
```

When disabled, the adapter is not registered and the "Azure OpenAI" option does
not appear in the providers dropdown.

## What to do when TestKey shows a deprecation warning

The harness surfaces deprecation warnings from the `api-deprecation` and
`azureml-model-deprecation` response headers when testing your API key.

If you see a warning like `"api-deprecation: 2025-06-01"`:

1. Check the [Azure OpenAI API retirement page](https://learn.microsoft.com/en-us/azure/ai-services/openai/api-version-deprecation)
   for the specific api-version being deprecated.
2. Update the `api_version` field in your `deployments.yaml` to a non-deprecated version.
3. Restart the harness so it picks up the new YAML.

## Reasoning Models (o1, o3)

Azure OpenAI deployments serving o1 or o3 models support the `reasoning_effort`
wire parameter. The harness maps `ReasoningSpec.BudgetTokens` to effort levels:

| BudgetTokens | reasoning_effort |
|---|---|
| 0 (unset) or ≥ 15000 | `high` |
| 4000 – 14999 | `medium` |
| 1 – 3999 | `low` |

Configure o1/o3 deployments with a **preview** api-version in `deployments.yaml`
(see recommendations above). The GA api-version does not expose `reasoning_effort`
for these models.

## Cost Accounting

Azure OpenAI usage is costed using the same token prices as the equivalent
OpenAI models (configurable via the starter price table override). The cost
appears in the per-session usage view and audit trail as `kind=azure-openai`.

## Capability Catalog

Azure OpenAI inherits the OpenAI YAML capability descriptors. The adapter
queries the catalog with `provider="openai"` and rewrites the Provider field
to `"azure-openai"` in audit payloads. This means:

- `gpt-4o*` deployments report `CapVision=true`, `CapStructuredOutput=true`.
- `o1*` deployments report `CapReasoning=true`.
- `dall-e-3` and `gpt-image-1` deployments report `CapImageOutput=true`.
