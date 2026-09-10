# Data rules

The onset estimate is **WHOOP sleep start − phone event time**, in elapsed minutes. It is a proxy, not a clinical sleep-latency measurement.

- Use the latest stored version of each sleep ID. Exclude naps, incomplete intervals, and future end times.
- Require a scored sleep before calculating onset. Select the latest unused phone event at or before sleep start, within four hours; ignore events recorded during another sleep. Four hours is a conservative application matching policy, not a WHOOP rule or clinical threshold.
- Join recovery by `sleep_id`, never position, creation date, or calendar day. Only scored recovery is displayed.
- Preserve timestamp offsets and compare absolute instants. The “today” label uses `USER_TIMEZONE` and local calendar boundaries across DST. Calendar days are not WHOOP physiological cycles.
- Report recorded interval and actual sleep separately. Actual sleep is light + slow-wave + REM; awake and no-sensor-data time are excluded.
- Fetch the object named by a webhook, even for historical edits. Recovery webhook IDs refer to sleep UUIDs; retrieve that sleep’s numeric cycle ID to fetch recovery.
- Store new sleep/recovery snapshots under stable IDs. Read legacy timestamp keys too, deduplicating by the latest sync timestamp. Delete events remove both legacy and current copies. Sync failures return an error so Lambda retries.
- Follow pagination on both WHOOP and DynamoDB. For this personal project, reports read the user’s full stored history before matching and applying the reporting window. A larger multi-user service should add indexed date-range access.

## References

- [WHOOP API reference](https://developer.whoop.com/api/): numeric cycle IDs, UUID sleep IDs, optional scores, collection boundaries and pagination.
- [WHOOP sleep data](https://developer.whoop.com/docs/developing/user-data/sleep/): score states and stage durations.
- [WHOOP cycles](https://developer.whoop.com/docs/developing/user-data/cycle/): physiological cycles do not necessarily align with calendar days.
- [WHOOP webhooks](https://developer.whoop.com/docs/developing/webhooks/): event identities, signatures, updates, deletes, and retries.
