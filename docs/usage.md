# Pages & API

Everything is read-only. Every page and response reflects a snapshot of the namespace that is at most `CACHE_TTL` old. If the latest refresh failed, the last good snapshot is served with a warning. If there is no snapshot at all, the answer is `503` and says why.

## Pages

### Overview — `/`

![Overview](images/overview.png)

- **Findings**, grouped by kind, worst first:
  - `pod-cannot-start`
  - `unread-stream`
  - `shared-consumer-group`
  - `uncovered-severity`
  - `broken-rule`
  - `profile-missing`
  - `profile-unresolved`
  - `streams-unknown`
  - `scaled-to-zero`
- **Streams**: the routed pitchers that publish to each stream and the catchers that read it, with their consumer groups. A stream nobody reads is highlighted, and each row links to its matrix and dry run.
- **Components**: every Deployment matched by the label selector, with kind, role, streams, consumer group (and where the value came from), profile status (ConfigMap / key), start problems and notes.

### Matrix — `/matrix?stream=<stream>`

![Matrix](images/matrix.png)

- **Rows:** the systems named by any rule of a catcher reading the stream, then `(other)` for every other system.
- **Columns:** the severities.
- **Cells:** each cell lists, per catcher, the rule that matches and what it does.
- **Gaps:** an entry is highlighted when a catcher does not react to a severity in `MUST_REACT_SEVERITIES`. A rule with a problem, such as an unknown effect, is outlined.

A cell evaluates a message carrying only that system and severity, so rules that also require tags or message text do not match there. Use the dry run for a concrete message.

Without `stream`, the page shows the first stream a catcher reads.

### Dry run — `/dryrun`

![Dry run](images/dryrun.png)

Enter a stream and a message (severity, system, title, author, tags, message text). The result lists, per catcher, whether it receives the message, whether it shares its consumer group, and what it does. When no catcher reads the stream, the result says **"This message reaches nobody"**.

Nothing is published. With JavaScript, htmx swaps the result in place; without it, the form posts and the full page renders.

## API

All endpoints return JSON with a `meta` object:

```json
{"meta": {"namespace": "homerun2", "labelSelector": "app.kubernetes.io/part-of=homerun2",
          "takenAt": "2026-09-12T05:22:56Z", "refreshError": "…", "refreshFailedAt": "…"}}
```

`refreshError` and `refreshFailedAt` only appear when the latest rebuild failed.

| Method & path | Returns |
|---|---|
| `GET /api/components` | `components`: kind, role, replicas, container, image, `streams` and the `streamValues` they came from, `consumerGroup`, `profilePath`, `profile` (`status`, `configMap`, `key`, `message`), `startProblems`, `notes` |
| `GET /api/findings` | `mustReactSeverities`, `findings` (`kind`, `component`, `stream`, `rule`, `severities`, `message`) |
| `GET /api/streams` | `streams`: `stream`, `pitchers`, `catchers` (`name`, `consumerGroup`) |
| `GET /api/matrix?stream=<s>[&severities=a,b]` | `matrix`: `catchers`, `systems`, `severities`, `cells` with `deliveries` per catcher |
| `POST /api/dryrun` | body `{"stream": "...", "message": {...}}` → `pitchers`, `reachesNobody`, `deliveries` (`component`, `receives`, `sharedWith`, `reactions`) |
| `GET /healthz` | `{"status":"healthy", "version", "commit", "date", "time"}`; does not call the Kubernetes API |

### Examples for the integration stage

```bash
VIEWER=https://homerun2-config-viewer.example.com

# Fail a pipeline on any finding
curl -s $VIEWER/api/findings | jq -e '.findings | length == 0'

# Who reads the stream a pitcher is configured with?
curl -s $VIEWER/api/streams | jq '.streams[] | select(.stream == "messages")'

# What would a critical alert from k8s do?
curl -s -X POST $VIEWER/api/dryrun \
  -d '{"stream":"messages","message":{"severity":"critical","system":"k8s","title":"node down"}}' \
  | jq '{reachesNobody, deliveries: [.deliveries[] | {component, reactions: [.reactions[]?.summary]}]}'

# Gaps for error and critical on a stream
curl -s "$VIEWER/api/matrix?stream=messages&severities=error,critical" | jq '.matrix.cells'
```

The `message` object uses homerun-library's `Message` fields: `title`, `message`, `severity`, `author`, `system`, `tags`, `url`, …

### Errors

| Status | When |
|---|---|
| `400` | `stream` missing or blank; unknown severity; malformed JSON; unknown fields; more than one JSON object |
| `413` | request body over 64 KiB |
| `405` | wrong method (with `Allow`) |
| `503` | no snapshot could be read (no client, RBAC denied, API unreachable since start) |

A dry run to a stream nobody reads is **not** an error: it answers `200` with `reachesNobody: true`.
