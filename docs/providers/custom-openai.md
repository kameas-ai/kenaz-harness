# Custom OpenAI-compatible endpoints

kenaz-harness can connect to any server that speaks the OpenAI chat-completion
wire format. This covers locally-hosted models (vLLM, llama.cpp, LiteLLM) and
hosted inference services that expose an OpenAI-compatible API (Together,
Groq, Fireworks, Anyscale, DeepSeek, Mistral AI).

---

## Feature flag

The custom-openai kind is enabled by default. To disable it (for example in
locked-down deployments), set:

```
HARNESS_CUSTOM_OPENAI=0
```

When the flag is `0`, the adapter is not registered, the "Custom
OpenAI-compatible" option is hidden from the Add Provider form, and the three
RPC methods (`ListCustomTemplates`, `RecognizeTemplate`,
`ProbeCustomEndpoint`) return an error.

---

## Shipped templates

Each template pre-fills the base URL and auth scheme for a well-known provider.
The harness matches the base URL you enter against the template registry via
glob matching and selects a template automatically (or you can force one from
the template chip picker).

| Template ID | Default base URL | Auth scheme | Notes |
|---|---|---|---|
| `vllm` | `http://localhost:8000` | none | vLLM local; no auth by default |
| `together` | `https://api.together.xyz` | bearer | Together AI |
| `groq` | `https://api.groq.com/openai` | bearer | Groq |
| `fireworks` | `https://api.fireworks.ai/inference` | bearer | Fireworks AI |
| `anyscale` | `https://api.endpoints.anyscale.com` | bearer | Anyscale Endpoints |
| `deepseek` | `https://api.deepseek.com` | bearer | DeepSeek |
| `mistral` | `https://api.mistral.ai` | bearer | Mistral AI |
| `llamacpp` | `http://localhost:8080` | none | llama.cpp server; no auth |
| `litellm` | `http://localhost:4000` | bearer | LiteLLM proxy |

---

## Three-step capability probe

After entering the base URL, model ID, and API key, clicking **Probe** runs a
lightweight three-step protocol against your endpoint:

1. **Streaming** — sends a minimal chat request with `stream: true`. Reports
   `true` if the server streams SSE frames, `false` if it returns a one-shot
   JSON blob, or `unknown` if the call fails.

2. **Tool calling** — sends a request with a single no-op tool definition.
   Reports `true` if the server returns a tool-call response, `false` if it
   returns a 400 with a message containing "tool" or "function" (indicating the
   endpoint does not support function calling), or `unknown` otherwise.

3. **Streaming usage** — repeats the streaming request and looks for a
   `usage` field in the final SSE frame. Reports `true` if present, `false` if
   absent.

The capability matrix is stored on the provider profile (SQLite migration 0331)
and re-checked whenever you click Probe again. The harness uses the matrix at
chat time to decide whether to include tool definitions and whether to expect
streaming usage tokens.

---

## Auth schemes

| Scheme | When to use |
|---|---|
| `bearer` | Standard `Authorization: Bearer <key>` header. Matches most hosted services. |
| `api-key-header` | Custom header name with the key value directly (no "Bearer" prefix). |
| `custom` | You supply both the header name and the key value. Use when a service uses a non-standard header such as `X-Org-Token`. |
| `none` | No authentication. Suitable for local servers (vLLM, llama.cpp) running without auth middleware. |

---

## Adding a provider

1. Open **Settings > Providers > Add Provider**.
2. Select **Custom OpenAI-compatible** from the Provider dropdown.
3. Choose a template chip if your service is in the list, or leave unset for
   an untemplated endpoint.
4. Enter the **Base URL** (e.g. `http://localhost:8000` for vLLM). Auto-
   recognition fires after a short pause and picks a matching template if one
   exists.
5. Enter the **Model ID** (e.g. `meta-llama/Llama-3-8b-instruct`).
6. Select an **Auth scheme** and paste your API key (or select `none` for
   local servers).
7. Click **Probe** to run the capability check. You can proceed without
   probing — the harness will record all capabilities as `unknown` and operate
   conservatively (no tools, no streaming usage).
8. Click **Add Provider**.

---

## Workbench (served) mode: host-granted endpoint

The served harness inside a Kenaz workbench has no Settings surface — its
single provider profile is seeded from the environment the control plane
writes onto the KENAZMETA disk (Spec 078). To point a workbench at an
OpenAI-compatible endpoint (for example a host-side `llama-server` reachable
over the vmnet gateway), grant:

| Env var | Required | Meaning |
|---|---|---|
| `KENAZ_HARNESS_PROVIDER=custom-openai` | yes | selects the custom-openai adapter |
| `KENAZ_HARNESS_BASE_URL` | yes | base URL, e.g. `http://192.168.64.1:8081/v1` |
| `KENAZ_HARNESS_MODEL` | yes | model id sent on every request (no default for this kind) |
| `KENAZ_HARNESS_CRED_ENV` | no | name of the env var holding the API key; omit for local servers |
| `KENAZ_HARNESS_AUTH_SCHEME` | no | `none` (default without a credential) / `bearer` (default with one) / `api-key-header` / `custom` |

The workbench's egress policy must permit the destination: the RFC1918
carve-out covers the vmnet gateway under the `local-only` tier; under
`allowlist` add the host:port explicitly.

At boot the served harness runs the same three-step probe against the granted
endpoint and records the verdict on the profile's capability hints (logged as
`harness.serve.host_provider.probed`). An unreachable endpoint logs
`harness.serve.host_provider.probe_failed` and leaves the hints unset, so the
adapter operates conservatively until the next boot. There is no Probe button
in served mode — restart the workbench to re-probe.

## Troubleshooting

**Probe returns "streaming: unknown"**

The server did not respond to the streaming request within 10 seconds, or
returned a non-200 status. Confirm the base URL is reachable and the server is
running. For vLLM, check that the model is loaded (`GET /v1/models`).

**Probe returns "tool calling: false"**

The endpoint does not support function calling. Tool nodes in the workflow
canvas will be skipped for this provider. Use a model that supports tool
calling (e.g. Llama-3.1 Instruct variants, Mistral Large).

**"Custom OpenAI-compatible" missing from Add Provider**

The feature is disabled via `HARNESS_CUSTOM_OPENAI=0`. Remove the env var or
set it to `1`.

**Connection refused on probe**

The local server is not running or is listening on a different port. Verify
with `curl <base_url>/v1/models`.
