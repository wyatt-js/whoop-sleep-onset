# Backend setup

The repository contains application code; AWS infrastructure must be provisioned separately. Install Go 1.25.5+ and `zip`, then build from source.

## AWS resources

1. Create a DynamoDB table named `sleep-onset-events` with string partition key `PK` and string sort key `SK`. Add a `bearer_token-index` GSI with string partition key `bearer_token` and all attributes projected. Enable streams with new images.
2. Create two **ARM64, provided.al2023** Lambda functions with 256 MB memory. Use a 30-second timeout for the API and 60 seconds for the sync worker. Build their packages with `make build-api build-sync` and upload the corresponding ZIP files in `bin/`.
3. Connect the table stream to the sync Lambda. Filter for inserted webhook rows (`PK` starts with `WHOOPUSER#`, `SK` starts with `WEBHOOK#`). Use a batch size of one, parallelization factor one, three retries, a one-hour maximum record age, and an encrypted SQS failure destination.
4. Give the API Lambda read/write access only to this table and index, plus `secretsmanager:GetSecretValue` for the `WSO` secret. Give the sync Lambda read/write/**DeleteItem** access to this table, read access to its stream, access to the same secret, and `sqs:SendMessage` for its failure queue. Both functions need CloudWatch logging and outbound HTTPS access.
5. Configure an API Gateway HTTP API with a deployed stage and Lambda proxy payload format **2.0**. Give API Gateway permission to invoke the API Lambda, then route these paths to it:
   - `GET /auth/whoop/start`
   - `GET /auth/whoop/callback`
   - `POST /phone-lock`
   - `POST /webhook/whoop`
   - `GET /last`

## Configuration

Create a Secrets Manager secret containing a JSON object with `WHOOP_CLIENT_ID` and `WHOOP_CLIENT_SECRET`. Set `SECRET_NAME` on both Lambdas. Give both roles access to that secret.

Set these additional API Lambda environment variables:

| Variable | Value |
| --- | --- |
| `WHOOP_REDIRECT_URI` | `https://YOUR_API_HOST/auth/whoop/callback` |
| `USER_TIMEZONE` | IANA timezone, default `America/New_York` |

Register the same redirect URI in your WHOOP developer app. Set the webhook URL to `https://YOUR_API_HOST/webhook/whoop`, using **v2** webhooks. The app requests `offline`, `read:sleep`, `read:recovery`, and `read:profile` scopes. Webhooks are verified with the WHOOP client secret.

OAuth uses a signed, Secure, HttpOnly cookie, so the API must use HTTPS. The CLI’s saved token is the application token displayed after sign-in, not a WHOOP access token. Keep it private.

## First data and verification

Authenticate, configure the CLI, and run the iOS automation once. After your next scored sleep, WHOOP sends update webhooks and the worker stores the records. `last` should show that sleep’s date and a matching phone event, or explain why onset is unavailable.

There is no automatic historical backfill or scheduled reconciliation job. To resync a specific older sleep, edit its start/end time in WHOOP and restore it; WHOOP documents this as a webhook test procedure. Missing webhook deliveries require resyncing. A new installation can legitimately show no data until the first event.

When restoring an existing deployment, deploy both Lambdas together, ensure the sync role has `DeleteItem`, and check the stream’s retry/failure settings. No table key schema change is required. Local tests use synthetic data; a live end-to-end check requires your running backend and new WHOOP records.
