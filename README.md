# Whoop Sleep Onset

A  Go CLI that measures sleep onset latency by combining iOS Shortcut phone-lock detection with WHOOP biometric data. Computes correlations between how long it takes you to fall asleep and next-day recovery, strain, and consistency.

## Why

WHOOP does not track sleep onset latency and when you attempt to fall asleep. It only tracks "sleep start time" in their app and exposes it via the developer API. This tool derives the metric independently via a timestamp when you put down your phone at night (via iOS Shortcut) minus WHOOP's detected sleep start and then layers correlation and Claude AI analysis.

## How It Works

1. **Phone-Down Detection** — An iOS Shortcut automation triggers when the iPhone connects to a charger during night hours (10pm–3am). It sends an HTTP POST with a timestamp to the API Gateway endpoint hosted with Amazon Web Services.

2. **WHOOP Data Ingestion** — After a WHOOP webhook event, a Lambda pulls sleep, recovery, and strain data from the WHOOP API.

3. **Sleep Onset Derivation** — The system computes the delta between the phone-lock timestamp and WHOOP's sleep start time. This is your sleep onset latency.

4. **Correlation Engine** — Goroutines fan out to concurrently compute correlations and visuals across metrics:
   - Sleep onset latency → next-day recovery score
   - Sleep onset latency → next-day consistency score
   - Previous day strain → onset latency
   - Onset latency trends over a 4 day window (WHOOP's window for calculating consistency)

5. **AI Insights** - Correlation data and trends are passed to Claude Sonnet to generate recommendations

## CLI Usage

```bash
# Add binary to PATH
sudo cp bin/sleeponset /usr/local/bin/

# Open browser to authenticate with WHOOP
sleeponset auth

# Save your token after authenticating
sleeponset configure --token <your-token>

# After a night of sleep
sleeponset last

# View correlations and visuals
sleeponset correlate

# Ask claude for recommendations
sleeponset insights
```

## iOS Shortcut Setup

1. Open **Shortcuts** app on iPhone
2. Go to **Automation** → **New Automation**
3. Select **Charger** → **Is Connected**
4. Toggle **Run Immediately** (no confirmation)
5. Add action: **If ANY** → Current Time is after 10:00 PM OR Current Time is before 3:00 AM
6. Add action: **Get Contents of URL**
   - URL: `https://ozls3538ce.execute-api.us-east-1.amazonaws.com/phone-lock`
   - Method: `POST`
   - Headers: `Authorization: Bearer <your-token>`

## Tech Stack

| Layer            | Technology                        |
|------------------|-----------------------------------|
| Language         | Go                                |
| CLI Framework    | Cobra + Viper                     |
| Database         | AWS DynamoDB                      |
| Compute          | AWS Lambda (2x)                   |
| API              | AWS API Gateway                   |
| AI Insights      | Claude Sonnet (Anthropic API)     |

## Project Structure

```
├── bin/
│   ├── api/                  # Cobra CLI (sleeponset)
│   │   ├── bootstrap         # Lambda binary for api routes
│   └── sync/
│   │   ├── bootstrap         # Lambda binary for WHOOP data syncing
│   └── sleeponset            # CLI Binary
├── cmd/
│   ├── cli/                  # Cobra CLI (sleeponset)
│   │   ├── main.go           # Root command and Viper config
│   │   ├── auth.go           # `auth` command — opens browser for WHOOP OAuth
│   │   ├── correlate.go      # Generate correlations and visuals across metrics
│   │   ├── insights.go       # Generate AI insights with the Anthropic API
│   │   ├── last.go           # `last` command — displays last night's sleep results
│   │   └── configure.go      # `configure` command — saves token/api-url
│   └── lambda-api/
│       └── main.go           # API Gateway Lambda handler (auth, phone-lock, webhooks)
├── internal/
│   ├── dynamo/
│   │   └── client.go         # DynamoDB client — users and phone-lock events
│   └── whoop/
│       ├── client.go         # WHOOP sleep/recovery fetching
│       ├── oauth.go          # WHOOP OAuth2 flow (state, token exchange)
│       └── profile.go        # WHOOP profile API
├── Makefile
├── go.mod
└── go.sum
```
