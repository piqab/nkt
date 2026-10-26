# LiteLLM

> **WARNING:** this example uses a third-party repository and images
> ([LiteLLM](https://github.com/BerriAI/litellm)) — their security and
> stability are not guaranteed.

One OpenAI-compatible address in front of any models — your own
(llama.cpp, Ollama, vLLM) and cloud ones (OpenAI, Anthropic…) — with
**virtual keys**: each key has its own models, budget, request and token
limits, expiry and spend. An app gets its own key, not the provider's.

- [`deploy/docker-compose.yml`](deploy/docker-compose.yml) — LiteLLM (a
  pinned image) and PostgreSQL for keys, models and spend.
- [`pipeline.yaml`](pipeline.yaml) — the pipeline: host `llm1`, an HTTPS
  site for the API.
- [`deploy/nkt-plugin.py`](deploy/nkt-plugin.py) — the hub plugin: keys,
  models, spend from About → Plugins.

## Deploying

Pipelines → Examples → **LiteLLM** → host, API domain. The hub creates:

- `LITELLM_MASTER_KEY` — the master key (for LiteLLM it's `sk-` + this
  value);
- `LITELLM_SALT_KEY` — the key LiteLLM encrypts provider keys with in its
  database. **Don't change it after the first deploy** — stored provider
  keys would become unreadable;
- `LITELLM_DB_PASSWORD` — the database password.

LiteLLM's own admin UI (`/ui`) is off: the hub plugin manages keys and
models. To turn it on — `LITELLM_DISABLE_ADMIN_UI: "False"` in the
pipeline's `env` (log in with the master key).

The hub's proxy waits up to 5 minutes for an answer; streaming answers
(`stream: true`) go through at once, unbuffered. `stream: true` is set by
the client in the request — the server can't turn streaming on for it;
ask for long generations as a stream.

**Through Traffic Fly.** `pipeline.yaml` has a commented `fly:` block:
the API goes out through a gateway (the gateway issues the certificate),
and the hub's DNS plugin sets the name's A record to the gateway's
address (`dns: plugin`). Uncomment it **instead of** `site:`, not
alongside. Ports 80 and 443 on the host itself aren't needed then. The
gateway waits up to 5 minutes for an ordinary answer to start; streaming
ones go through at once.

## Hub plugin

About → Plugins → **Install on a host** → LiteLLM → the host LiteLLM is
deployed on → the master key (from the pipeline's `.env`; the plugin adds
`sk-` itself). Service `nkt-plugin-litellm`, 127.0.0.1:9210, no Docker.

- **Models** — Add a model: a name for clients (`qwen`), the provider
  model (`openai/qwen3-8b` — any OpenAI-compatible one: llama.cpp, Ollama,
  vLLM; `anthropic/claude-sonnet-4-5`, `openai/gpt-4o`), the API address
  and the provider key (a masked field; LiteLLM stores it encrypted).
  Models added this way can be deleted; ones from the config file can't.
- **Keys** — Create a key: a name, models (empty — all), a budget in
  dollars and when it resets, request and token limits per minute, expiry.
  - **In the window** a new key is shown **once** with a Copy button —
    neither the hub nor the plugin shows it again.
  - **Straight into a pipeline `.env`** — the “Straight into a pipeline
    .env” and “Variable name” fields (`LLM_API_KEY`): the key isn't shown,
    the hub writes it into the app pipeline's `.env` (with version
    history); it reaches the stack with the next deploy.
  - Block / Unblock, Delete.
- **Spend** — per key and in total, since each key was created, at
  LiteLLM prices (your own models cost 0).
- **Scripts** — `llm keys`, `llm key block|unblock|rm NAME`, a new key
  only straight into a `.env`:
  `llm key add videolog models qwen budget 20 budget-reset 30d to-pipeline videolog env LLM_API_KEY`.
- **MCP** — `litellm_*`. An agent creates a key only with `to_pipeline`
  and `env` — it never sees the value.

Rights: viewing — everyone, changes — an admin or a token with the
“plugin litellm” action; writing a key into a pipeline `.env` also needs
the right to edit that pipeline (an admin or a token with
“pipeline.create” within its limits).

## The app

```
OPENAI_BASE_URL=https://llm.example.com/v1
OPENAI_API_KEY=${LLM_API_KEY}
```

The model — by its name for clients: `"model": "qwen"`.

## Testing

`python3 -I deploy/test_plugin.py` — the plugin against a fake LiteLLM
API (no Docker); `go test ./examples` runs it too. Among other things it
checks that keys never reach the service log.
