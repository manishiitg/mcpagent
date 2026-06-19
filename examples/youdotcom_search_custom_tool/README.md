# You.com Search Custom Tool Example

This example adds a `you_search` custom tool to MCPAgent using direct HTTP calls to the you.com Search API.

## Why this example

- shows a clean provider option without adding SDK dependencies
- works with `YDC_API_KEY` and has an unauthenticated free-tier fallback path
- demonstrates practical error handling for auth/rate limits and empty results

## Setup

```bash
cd examples/youdotcom_search_custom_tool
go mod tidy
```

Create `.env` (or export vars in your shell):

```bash
OPENAI_API_KEY=your-openai-key
# optional, recommended for stable usage
YDC_API_KEY=your-youcom-key
```

Notes:
- Search API supports 100 free requests/day without `YDC_API_KEY`
- for consistent production behavior, set `YDC_API_KEY`

## Run

```bash
go run main.go
```

Or pass your own prompt:

```bash
go run main.go "Use you_search to find the latest model context protocol updates and summarize in 5 bullets"
```

## Tool contract

`you_search` accepts:
- `query` (string, required)
- `count` (int, optional, defaults to 5, clamped 1..20)

The tool calls:
- `GET https://api.you.com/v1/agents/search?query=...&count=...`
- Header: `X-API-Key: $YDC_API_KEY` when set

## Fallback and error behavior

- If no key is set and API returns `401`, `403`, or `429`, the tool returns a friendly fallback message instructing to set `YDC_API_KEY`
- Empty `results.web` returns a non-error informational response
- Non-2xx responses include status/body context for debugging
