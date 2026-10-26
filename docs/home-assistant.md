# Home Assistant with NestWall

NestWall exposes a small summary for dashboards and Home Assistant. It is a read-only REST endpoint, and an API token authorises it.

## 1. Create a token

1. Open **Settings > Integrations** in NestWall.
2. Select **Create token** and give it a name, for example `Home Assistant`.
3. Keep both scopes selected: `tasks:read` and `calendar:read`. The summary needs both, so a token with only one gets 403.
4. Choose an expiry and create the token. Copy it now. NestWall shows it only once.

A token can read only what its owner can already see. If the owner loses the `tasks.view` or `calendar.view` permission, the token stops working. You can revoke a token at any time from the same page.

## 2. Add the sensor

Add this to `configuration.yaml`, replacing the host with the address Home Assistant uses to reach NestWall:

```yaml
rest:
  - resource: http://nestwall.local:8080/api/v1/integrations/summary
    scan_interval: 300
    headers:
      Authorization: !secret nestwall_bearer
    sensor:
      - name: "NestWall tasks due today"
        value_template: "{{ value_json.data.tasks_due_today }}"
```

Then add the secret to `secrets.yaml`. Keep the word `Bearer` and the space before the token:

```yaml
nestwall_bearer: "Bearer nst_xxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

Restart Home Assistant or reload the REST configuration. The sensor appears as `sensor.nestwall_tasks_due_today`.

## 3. Response

`GET /api/v1/integrations/summary` returns the data under `data`:

```json
{
  "data": {
    "date": "2025-01-01",
    "timezone": "Europe/London",
    "tasks_due_today": 6,
    "tasks_done_today": 2,
    "tasks_overdue": 1,
    "tasks_waiting_approval": 1,
    "events_today": 2,
    "next_events": [
      {
        "title": "Football practice",
        "date": "2025-01-01",
        "starts_at": "2025-01-01T17:00:00Z",
        "all_day": false
      }
    ]
  }
}
```

- Dates use the family's time zone.
- Task counts come from the parent dashboard, so skipped copies are left out.
- `next_events` holds up to 5 occurrences from today over the next 7 days, soonest first.

## Errors

| Status | Meaning |
|---|---|
| 401 `unauthenticated` | No `Authorization` header, or a malformed token. |
| 401 `token_invalid` | The token is unknown, revoked or expired. |
| 403 `insufficient_scope` | The token lacks `tasks:read` or `calendar:read`, or its owner no longer has the permission. |
