# WHOOP Sleep Onset

A Go CLI that estimates the time between putting your phone down and WHOOP’s recorded sleep start. An iOS Shortcut logs bedtime intent; an AWS backend pairs it with sleep and recovery data.

- **`last`** shows your latest main sleep, estimated onset, sleep stages, and matched recovery.
- Missing nights and pending scores stay unavailable instead of becoming misleading numbers.

This is a personal tracking estimate: connecting a charger does not prove you stopped using your phone, and WHOOP’s sleep start is not a clinical measurement of sleep onset.

## Setup

Requires Go 1.25.5+, an iPhone, a WHOOP account, and an AWS account. The repository does not provision AWS automatically.

1. Deploy the two Lambda functions, DynamoDB table, and API Gateway routes in the [backend setup guide](docs/setup.md). Copy the resulting HTTPS API URL.
2. Build and connect the CLI:

```sh
make build-cli
./bin/sleeponset configure --api-url https://YOUR_API_HOST
./bin/sleeponset auth
```

3. After WHOOP sign-in, save the application token shown in the browser:

```sh
./bin/sleeponset configure --token YOUR_APP_TOKEN
```

4. On your iPhone, create a **Charger → Is Connected** automation that runs immediately during your bedtime hours. Add **Get Contents of URL**: `POST https://YOUR_API_HOST/phone-lock`, with the header `Authorization: Bearer YOUR_APP_TOKEN`. No body is required.
5. After WHOOP processes your next sleep, run `./bin/sleeponset last`.

## How matching works

Each completed, non-nap sleep uses the closest phone event in the preceding four hours. Recovery is joined by WHOOP sleep ID. Unmatched events are left out; older results show their date.

Built with **Go, AWS Lambda, DynamoDB, API Gateway, and WHOOP API v2**. The CLI, API, and webhook worker live in `cmd/`; matching and API clients live in `internal/`.

```sh
go test ./...
go vet ./...
```

Tests cover midnight and daylight-saving transitions, missing nights, naps, pagination, historical webhooks, and authentication. See [data rules and API references](docs/data-rules.md) for details.
