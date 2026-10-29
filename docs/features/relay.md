# Relay

A relay is the only way a client reaches a Mac running Zubi: both open a WebSocket to it, and it
copies messages between the two. The app uses `wss://relay.fa-krug.de` (`BridgeController.defaultRelay`)
unless the user enters their own under Settings › Connection; `zubi relay` is that relay's code. Inside, each client and the Mac
run a Noise handshake keyed with the client's device key, so everything after it is end-to-end
encrypted and the relay only ever copies ciphertext.

| Part | Code |
| --- | --- |
| The relay server, certificates, STUN | [`cli/internal/relay`](../../cli/internal/relay), [`cli/cmd/zubi/relay.go`](../../cli/cmd/zubi/relay.go) |
| WebSockets (RFC 6455, both sides) | [`cli/internal/websocket`](../../cli/internal/websocket) |
| Noise, in Go | [`cli/internal/noise`](../../cli/internal/noise) |
| Sessions and pairing codes, the client's side, and a Go host for tests | [`cli/internal/tunnel`](../../cli/internal/tunnel), [`cli/cmd/zubi/pair.go`](../../cli/cmd/zubi/pair.go) (`zubi pair`, `zubi serve`), [`cli/cmd/zubi/status.go`](../../cli/cmd/zubi/status.go) (`zubi`) |
| The Mac's side: Noise in Swift, the room, sessions into Zubi's own server | [`Packages/ZubiAPI/Sources/ZubiAPI/Relay`](../../Packages/ZubiAPI/Sources/ZubiAPI/Relay), wired up in `BridgeController` |
| Settings | status, the first client's code and the relay in `ServerPane` (Connection), clients and their codes in `KeysPane` (Clients) |

How to pair a device is in [cli/README.md](../../cli/README.md#get-started), and how to run and
host your own relay [there too](../../cli/README.md#your-own-relay).

## Invariants

- **The relay never holds a key and never reads what it copies.** Each session is end-to-end
  encrypted between the Mac and the client (Noise, keyed with the device key), so a relay, or whoever
  runs it, learns only who connects, when, and how much. Nothing in `internal/relay` may need to
  understand a data message.
- **A session reaches Zubi's own server, nothing else.** The Mac decrypts a session into a TCP
  connection to its own server on `127.0.0.1:8787`, which nothing else can reach, so every request still passes
  the bearer-key check, the Off/Read/Read & Write settings and the activity log. The handshake only
  proves the client holds some current key; the request's `Authorization` header, which `zubi`
  fills with the same key, decides what it may do.
- **Both Noise implementations are held to the same vectors**,
  [`relay-noise-vectors.json`](../../Packages/ZubiAPI/Tests/Fixtures/relay-noise-vectors.json),
  generated with an independent implementation (github.com/flynn/noise). A change to either one, or
  to the prologue, key or pairing format, changes that file and both test suites.
- **Clients only use wss:// relays.** A pairing code with a `ws://` relay is refused; the Mac only
  connects to `wss://`.
- **No silent fallback to plain.** Without a certificate the relay doesn't start: `Obtain` failing
  exits with 1 and the reason. Plain `ws://` takes `--no-tls`. `--behind-tls-proxy` serves plain HTTP
  for a proxy that terminates TLS, and refuses every `/v1` WebSocket whose `X-Forwarded-Proto` isn't
  `https`.
- **A room is its Ed25519 public key**, and only the holder of the private key can register it: the
  host signs `zubi-relay-v1/register` followed by a fresh 32-byte nonce from the relay. The newest
  valid registration wins, so a Mac that reconnects after a network change replaces its own stale
  connection.
- **A session ID is the only authorization for `/v1/accept`.** It is 128 random bits, sent only to the
  host over its control connection, and spent on first use.
- **Certificates come from ACME's TLS-ALPN-01 challenge only**, on the listener that serves the relay,
  so one port is all it needs. An IP address certificate uses Let's Encrypt's `shortlived` profile, the
  only one that covers IP addresses.
- **`golang.org/x/crypto` is the CLI's only dependency, and only `internal/relay` imports it** (for
  `acme`). The rest of the CLI stays standard library only.

## Protocol, version 1

Every endpoint is a WebSocket on `GET`, except `/` and `/health`, which answer plain text. The version
is in the path, so a later protocol can live beside this one.

| Endpoint | Who | What |
| --- | --- | --- |
| `GET /health` | platforms' health checks | `ok` |
| `GET /v1/host` | the Mac | its control connection: register a room, hear about sessions |
| `GET /v1/connect/{room}` | a client | a session with the room's host; 404 when no host has the room |
| `GET /v1/accept/{session}` | the Mac | its end of one session; 404 when unknown, timed out or spent |

### The host's control connection

Control messages are JSON objects in text messages, at most 4 KiB.

1. The relay sends `{"type":"challenge","nonce":"<32 bytes, base64url>"}`.
2. Within 10 seconds the host sends `{"type":"register","key":"<Ed25519 public key, base64url>",
   "signature":"<base64url>"}`, the signature being over the ASCII bytes `zubi-relay-v1/register`
   followed by the 32 nonce bytes. base64url is RFC 4648 §5, without padding.
3. The relay answers `{"type":"registered","room":"<the key>"}`, or `{"type":"error","message":…}`
   and closes with 4003.
4. For each client, the relay sends `{"type":"session","id":"<session ID>"}`; the host opens
   `/v1/accept/<id>` within 10 seconds.
5. The host sends `{"type":"ping"}` at least every 30 seconds and gets `{"type":"pong"}`. A host
   silent for 90 seconds is disconnected; the pings also keep platforms that sleep idle services
   awake. Unknown message types are ignored, both ways.

### Sessions

A client connects to `/v1/connect/{room}` and may send at once: its messages wait in the connection
until the host accepts. From then on the relay copies every message, text or binary, up to 65,535
bytes (a Noise message's limit), each way unchanged. It pings both ends every 30 seconds and drops an
end that stays silent for 75. When either end closes, the relay closes the other with the same code
and reason, when RFC 6455 lets that code be sent.

### Inside a session

The relay copies messages unchanged; this is what the Mac and the client put in them.

1. **The client's first message** is binary: the byte `1` (the version), one byte giving the length
   of the key's ID, the key's ID (UTF-8, at most 255 bytes), then the first Noise handshake message.
   The ID is in the clear so the Mac knows which key's secret to use; it is a random UUID.
2. **The handshake** is `Noise_NNpsk0_25519_AESGCM_SHA256`:
   - prologue: `zubi-relay-v1`, a zero byte, the room, a zero byte, the key's ID;
   - pre-shared key: SHA-256 of `zubi-relay-v1 psk`, a zero byte and the key's secret.

   The Mac answers with the second handshake message as one binary message. A Mac that doesn't know
   the key, or whose handshake fails because the client doesn't hold that secret, closes with 1008
   instead (the app's WebSocket API can send no code of its own), and `zubi` reports the key as
   refused. A client that can't read the answer knows it didn't come from the Mac holding the key.
3. **Then every message is one Noise transport message**: binary, at most 65,535 bytes, so at most
   65,519 bytes of the TCP stream to or from Zubi's API. An empty message is its sender's end of
   stream, like TCP's FIN: the other side finishes its own sending and then closes. Closing the
   WebSocket with 1000 ends the session at once.

### Pairing codes

A pairing code is `zubi1:` and base64url (without padding) of a JSON object with `relay` (the
`wss://` URL), `room`, `key_id`, `secret` and `name` (the key's, for display). It carries the key's
secret, so it is as secret as the key: Settings › Clients copies it for each client (Settings ›
Connection shows the first one's), and `zubi` checks it by asking the Mac, through the relay, which key
it is before saving it.

### The Mac

The room key is an Ed25519 key made on first use and kept in the Keychain next to the device keys
(account `relay-room-key`), so the room, and every pairing code, outlives restarts and updates. Zubi
Dev has its own, like its own keys. The relay connection runs whenever the server runs; it reconnects
after a failure, waiting 1 second, then twice as long each time, up to a minute. When it fails,
`RelayHost.diagnose` turns the error into one line for the status: whether anything answers at the
address, whether it is a Zubi relay, and what a proxy in front of it said.

### The client

`zubi` with no arguments pairs the first time and then shows the status: the pairing, the
`zubi-client` service, and whether the Mac answers a request through the relay. On Linux with systemd
it sets up `zubi-client.service`, which runs `zubi serve`: a Unix socket, `zubi.sock` next to the
config file and mode 0600, where each connection becomes one session through the relay. Every command
tries the socket first (200 ms) and otherwise dials the relay itself, so the service only saves the
connection time. `zubi serve` reads the pairing per session, so pairing again needs no restart, and
`zubi unpair` removes the service.

### Close codes

| Code | Sent to | Meaning |
| --- | --- | --- |
| 4000 | host | a newer connection registered the same room |
| 4001 | client | the host didn't accept the session in time |
| 4002 | client | the host disconnected before accepting |
| 4003 | host | the registration was malformed or its signature didn't verify |
| 1008 | client | from the Mac, during the handshake: the key was refused |
| 1013 | either | the relay is full (1,000 rooms) or shutting down: try again later |
| 1001 | either | the relay is shutting down, or the other end disappeared |

### Limits

Per client address (behind a proxy, the last `X-Forwarded-For` entry, which the proxy appended
itself): 2 new WebSockets a second with bursts of 30, and 64 open at once. Per room: 32 sessions
pending or open. A refused handshake answers 403 (behind a proxy, not over TLS), 404, 429 or 503,
with a one-line reason in the body.

## Certificates

`zubi relay` with no flags certifies this network's public IPv4 address: it asks a STUN server
(Cloudflare's, then Google's) which address its network has, starts the TLS listener, and orders a
certificate for that address from Let's Encrypt. It answers the TLS-ALPN-01 challenge on the same
listener: a validator asks for the `acme-tls/1` protocol and gets the challenge certificate, whatever
server name it sends (RFC 8738 sends the address's reverse-DNS name; an ordinary client connecting to
an IP address sends none). `--ip` names the address instead, and `--domain` certifies a name, with
Let's Encrypt's default profile.

- **Renewal**: every hour, the relay renews a certificate two thirds of the way through its life
  (after four of a six-day certificate's days) and, with neither flag, asks STUN again and gets a new
  certificate when the address changed. A failure is logged and retried after six minutes, while the
  old certificate stays in use.
- **State** (`--state`, default `~/.config/zubi/relay`): the ACME account key and the certificate,
  each mode 0600. A restart reuses a certificate that covers the address and isn't due for renewal,
  so restarts don't count against Let's Encrypt's limits. Another `--acme-directory` keeps its own
  account in a subdirectory.
- **Failure**: the CA must reach port 443 of the address. When it can't, the error says so and the
  relay exits; Let's Encrypt allows five failed validations per address an hour, so a service manager
  should wait between restarts: the unit `--install-service` writes waits 15 minutes in that mode.
- `x/crypto/acme`'s `autocert` doesn't fit: it rejects connections that carry no server name and only
  orders certificates for DNS names. `CertManager` drives `acme.Client` itself, with
  `CreateCertFromOrder` (not `CreateOrderCert`, which fails against a CA that finalizes
  asynchronously).

## Tests

`go test ./...` in `cli/` covers the WebSocket framing, the protocol (registration, pairing,
replacement, timeouts, limits, proxy mode), STUN, Noise against the shared vectors, and whole
sessions: the CLI, pairing, `zubi serve` and `zubi watch` against the fake Zubi, through a real relay
and the Go host. `swift test` in `Packages/ZubiAPI` holds the app's Noise, key derivation and pairing codes to
the same vectors. Nothing in CI runs the app against a relay, so a change to `RelayHost` needs a run
by hand: a relay with a certificate the Mac trusts (the app only connects to `wss://`), the app with
its own relay set to it, `zubi pair` with a code from Settings › Clients, then `zubi`, `zubi lists` and
`zubi watch`. The certificate tests run against Pebble, Let's
Encrypt's test CA, when `ZUBI_PEBBLE` names its binary; CI installs it:

```sh
go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1
ZUBI_PEBBLE="$(go env GOPATH)/bin/pebble" go test ./internal/relay/
```
