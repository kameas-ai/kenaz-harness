# Gemini Adapter — Manual Smoke Tests

This checklist verifies the Gemini adapter end-to-end against real Google
infrastructure. Run these steps after deploying a build that includes the
`gemini` adapter. No automated tests cover real GCP traffic.

## Prerequisites

- A Google AI Studio API key **or** a Vertex AI service account with
  `roles/aiplatform.user`.
- The harness running with `HARNESS_GOOGLE_GEMINI` unset (default: enabled).

## Steps

### 1. Key probe (AI Studio)

1. Open **Providers** → **Add Provider** → **Google Gemini** → **AI Studio**.
2. Paste a valid API key and click **Submit**.
3. Expected: provider card shows a green "Key valid" badge within 5 seconds.
4. Repeat with an invalid key (e.g. `invalid-key-12345`).
5. Expected: red badge reading "Unauthorized".

### 2. Streaming text generation

1. Open a new session and select a `gemini-2.0-flash` model.
2. Send the message: `"Count from 1 to 5, one number per line."`
3. Expected: five lines stream in progressively (not rendered all at once).
4. Confirm usage shows `input_tokens > 0` and `output_tokens > 0`.

### 3. Vision (multimodal input)

1. In the same session, attach any PNG or JPEG image (not a GIF).
2. Send: `"Describe what you see in the attached image in one sentence."`
3. Expected: a one-sentence description that references the image content.
4. Try attaching a GIF — expected: an error banner stating the MIME type is
   not supported.

### 4. Tool calling

1. Enable the `search` or `calculator` built-in tool in the session settings.
2. Send a question that requires the tool (e.g. `"What is 123 * 456?"`).
3. Expected: a tool-call badge appears in the message thread; the final
   assistant turn contains the computed answer.

### 5. Reasoning (gemini-2.5-flash or gemini-2.5-pro)

1. Switch the session model to `gemini-2.5-flash`.
2. Enable the reasoning dial (budget: 2048 tokens).
3. Send: `"Prove that the square root of 2 is irrational."`
4. Expected: the response includes a structured proof; the usage breakdown
   shows `reasoning_tokens > 0`.

### 6. Vertex AI key probe (optional)

1. Open **Providers** → **Add Provider** → **Google Gemini** → **Vertex AI**.
2. Choose **Application Default Credentials**, enter a valid project ID and
   region (e.g. `us-central1`).
3. Click **Submit** (no key probe is performed — credentials validate at
   first inference).
4. Start a session using the Vertex AI profile and repeat step 2.
5. Expected: text streams successfully; usage is attributed to the correct
   project in Google Cloud Billing.

## Pass Criteria

All five steps (or six, if Vertex is available) must complete without error
banners. Streaming must be progressive (partial tokens visible before the
response completes). Usage metadata must be non-zero on every inference.
