---
title: Webhook signing
status: accepted
last-reviewed: 2026-04-26
---

# Webhook signing

Customer webhooks are signed so receivers can verify authenticity and
reject replays.

## Headers

```
X-Video-Signature: sha256=<hex_hmac>
X-Video-Timestamp: <unix_seconds>
X-Video-Event-Type: <event_type>
X-Video-Delivery-Id: <delivery_ulid>
Content-Type: application/json
```

## Signature

```
signed_payload = `${X-Video-Timestamp}.${request.body}`
hex_hmac       = hex(HMAC-SHA256(endpoint.secret, signed_payload))
X-Video-Signature = `sha256=${hex_hmac}`
```

The `endpoint.secret` is per-endpoint, generated at endpoint-creation time,
returned to the customer exactly once, and stored plaintext in
`app.webhook_endpoints.secret` (it's a symmetric secret the platform must
have access to in order to sign).

## Verification (receiver side)

```ts
import { createHmac, timingSafeEqual } from 'node:crypto';

function verify(req, secret) {
  const sig       = req.headers['x-video-signature'];           // 'sha256=abc123...'
  const timestamp = req.headers['x-video-timestamp'];           // '1730000000'
  const body      = req.rawBody;                                // raw bytes BEFORE JSON parse

  // Replay protection: reject if timestamp is older than 5 minutes.
  const ageSec = Math.floor(Date.now() / 1000) - parseInt(timestamp, 10);
  if (ageSec > 300 || ageSec < -60) throw new Error('expired');

  const expected = 'sha256=' + createHmac('sha256', secret)
    .update(`${timestamp}.${body}`)
    .digest('hex');

  if (!timingSafeEqual(Buffer.from(sig), Buffer.from(expected))) {
    throw new Error('signature mismatch');
  }
}
```

Receivers must:

- Use the **raw request body** (bytes before JSON parsing). Any reformatting
  invalidates the signature.
- Compare with **timing-safe** equality.
- Enforce the **5-minute tolerance window** for replay protection.

## Reference receiver snippets

The platform docs ship snippets for common stacks:

- Node.js / Express
- Node.js / Fastify
- Python / Flask
- Go / net/http
- Ruby / Sinatra

(Land in the customer-facing portal docs post-MVP. MVP customers get the
TypeScript snippet above.)

## Retry policy

Failed deliveries (any non-2xx response, or timeout > 10s) retry with
exponential backoff:

| Attempt | Delay before retry |
|---|---|
| 1 | immediate |
| 2 | 30 seconds |
| 3 | 2 minutes |
| 4 | 10 minutes |
| 5 | 1 hour |
| 6 | 6 hours |
| 7+ | dropped (status: `failed`) |

Each attempt re-signs with the *current* timestamp (so the receiver's
replay protection still holds for retries).

## Delivery audit

Every delivery attempt persists to `app.webhook_deliveries` with
`status ∈ {pending, delivered, failed, dropped}`. Customers can later query
their delivery log via the customer-facing portal (post-MVP).

## Endpoint configuration

```http
POST /v1/webhook-endpoints
Authorization: Bearer sk_live_...
Content-Type: application/json

{
  "url": "https://customer.example.com/webhooks",
  "event_types": ["video.asset.ready", "video.asset.errored",
                  "video.live_stream.active", "video.live_stream.ended",
                  "video.live_stream.recording_ready"]
}
```

Response (returns secret exactly once):

```json
{
  "id": "wh_01HXY...",
  "url": "https://customer.example.com/webhooks",
  "secret": "whsec_<32 random base32>",
  "event_types": [...],
  "created_at": "2026-04-26T...",
  "enabled": true
}
```

After creation, GETs return everything except `secret`.

## Event catalog

The worker emits a small set of internal-callback events to whichever shell
dispatched the work — `transcode.complete`, `stream.live`,
`stream.recording_finalized`, etc. The complete catalog (including
shell-emitted, customer-facing webhook events) is owned by the consuming
shell, not this repo. See [`../design-docs/internal-callback-api.md`](../design-docs/internal-callback-api.md)
for the worker → shell payloads.

## Security considerations

- Webhook secrets are stored plaintext in DB (we need them to sign). Treat
  the DB as a secret-bearing system.
- Webhook URLs from customers can point anywhere (potentially to internal
  networks). Operators should configure outbound HTTP from the delivery
  worker to deny private IP ranges (`10.0.0.0/8`, `172.16.0.0/12`,
  `192.168.0.0/16`, `127.0.0.0/8`, `169.254.0.0/16`). Documented in
  `docs/operations/security-review.md` (lands in plan 0007).
- The webhook delivery worker should run with limited egress (no DNS over
  arbitrary ports, no UDP, etc.).
