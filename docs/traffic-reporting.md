# Persistent traffic reporting

Each panel node has a separate JSON queue identified by panel URL, node type and
node ID. The API key is not written to the queue and key rotation does not
discard pending usage.

At every panel push interval:

1. Snapshot user counters without resetting them.
2. Add the snapshot to the queue using a temporary file, file sync and atomic
   rename. If saving fails, leave counters intact and log the error.
3. Subtract only the saved bytes. Bytes arriving during persistence stay in the
   counters for the next collection.
4. Send accumulated pending usage to `/api/v1/server/UniProxy/push`. A successful
   response must be HTTP 2xx with the V2board JSON acknowledgement `data: true`.
5. Remove acknowledged usage from the queue. Network errors, non-success HTTP
   responses and invalid acknowledgements retain the queue for the next cycle.

Retries also run when there is no new usage. Pending records keep their original
UID even if a user is removed from the active list. Traffic is collected before
user removal, node reload and graceful controller shutdown.

## Storage

On Linux the default directory is `/var/lib/V2bX/traffic`. Elsewhere it is the
operating system's user cache directory under `V2bX/traffic`. Override the
directory per node with `TrafficStorePath` inside its `Options`, for example:

```json
"Options": {
  "Core": "xray",
  "TrafficStorePath": "/var/lib/V2bX/traffic",
  "ReportMinTraffic": 0
}
```

The installer preserves this directory across updates. Containers must mount a
persistent volume at `/var/lib/V2bX/traffic`. The service user needs write access.
Do not run two V2bX processes against the same queue or configure the same panel
node more than once. Corrupt queue files cause startup to fail rather than being
silently deleted. Back up the queue before attempting repairs.

`ReportMinTraffic` remains a per-user reporting threshold in KiB. Below-threshold
usage is now stored too, and accumulates until its total exceeds the threshold.
Queue size grows primarily with the number of users, rather than outage length,
because usage is aggregated per UID.

## Delivery limits

This provides **at-least-once delivery**, not exactly-once accounting. The panel
endpoint has no idempotency key: if it applies a request but its response is lost,
the next retry can count the same usage again. A crash after the panel accepts
usage but before the local acknowledgement is durably saved has the same risk.
Exactly-once delivery requires a matching change to the panel API/database.
If saving an acknowledgement fails while the process remains running, the next
cycle retries that local write without resending the accepted batch.

An abrupt kill or power loss can lose new traffic since the last collection.
Previously saved queued usage survives process restarts. If the panel is down
at startup, the existing startup requirement to fetch node/user configuration
still applies: the saved queue is retained, but serving users cannot resume until
the panel is reachable. Changing panel URL/type/node ID selects a different
queue; migrate old pending records deliberately rather than deleting them.

## Releases

The `Build and Release` workflow builds archives and SHA-256 checksum files.
Publishing a `v*` Git tag uploads them to a release in **this repository**.
Use `bash install.sh vVERSION` to select a version explicitly, or omit the
argument for the latest release. The installer checks the checksum and finishes
downloads before stopping an existing installation.
