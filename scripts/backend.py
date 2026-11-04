#!/usr/bin/env python3
"""Check, protect, and deploy the existing backend through AWS CLI v2."""

import argparse
import base64
import hashlib
import hmac
import json
from pathlib import Path
import subprocess
import sys
import secrets
import time
import uuid
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[1]
CONFIG = json.loads((ROOT / "infra/backend.json").read_text())
ROUTES = ["GET /auth/whoop/start", "GET /auth/whoop/callback",
          "POST /phone-lock", "POST /webhook/whoop", "GET /last"]
FILTER = {"eventName": ["INSERT"], "dynamodb": {"NewImage": {
    "PK": {"S": [{"prefix": "WHOOPUSER#"}]},
    "SK": {"S": [{"prefix": "WEBHOOK#"}]}}}}


def aws(*args):
    result = subprocess.run(
        ["aws", *args, "--profile", CONFIG["profile"], "--region", CONFIG["region"],
         "--output", "json", "--no-cli-pager"],
        capture_output=True, text=True, check=False)
    if result.returncode:
        raise RuntimeError(result.stderr.strip())
    return json.loads(result.stdout) if result.stdout.strip() else {}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def identity():
    caller = aws("sts", "get-caller-identity")
    require(caller["Account"] == CONFIG["account_id"], "AWS account differs from infra/backend.json")
    print(f"AWS account {caller['Account']}, region {CONFIG['region']}", flush=True)


def function(name):
    return aws("lambda", "get-function-configuration", "--function-name", name)


def alarms():
    for key in ("api_function", "sync_function"):
        name = CONFIG[key]
        yield name + "-errors", "AWS/Lambda", "Errors", "Sum", 0, "FunctionName", name
    name = CONFIG["sync_function"]
    yield name + "-iterator-age", "AWS/Lambda", "IteratorAge", "Maximum", 300000, "FunctionName", name
    yield name + "-destination-failures", "AWS/Lambda", "DestinationDeliveryFailures", "Sum", 0, "FunctionName", name
    queue = CONFIG["failure_queue"]
    yield queue + "-not-empty", "AWS/SQS", "ApproximateNumberOfMessagesVisible", "Maximum", 0, "QueueName", queue
    yield "WSO-api-server-errors", "AWS/ApiGateway", "5xx", "Sum", 0, "ApiId", CONFIG["api_id"]


def protect():
    # These operations preserve the existing table, items, routes, and roles.
    table = aws("dynamodb", "describe-table", "--table-name", CONFIG["table"])["Table"]
    if not table.get("DeletionProtectionEnabled"):
        aws("dynamodb", "update-table", "--table-name", CONFIG["table"], "--deletion-protection-enabled")
        aws("dynamodb", "wait", "table-exists", "--table-name", CONFIG["table"])
    aws("dynamodb", "update-continuous-backups", "--table-name", CONFIG["table"],
        "--point-in-time-recovery-specification", "PointInTimeRecoveryEnabled=true,RecoveryPeriodInDays=35")
    for name, namespace, metric, statistic, threshold, dimension, value in alarms():
        aws("cloudwatch", "put-metric-alarm", "--alarm-name", name,
            "--alarm-description", "WHOOP Sleep Onset backend; inspect CloudWatch and the sync failure queue.",
            "--namespace", namespace, "--metric-name", metric,
            "--statistic", statistic, "--period", "300", "--evaluation-periods", "1",
            "--threshold", str(threshold), "--comparison-operator", "GreaterThanThreshold",
            "--dimensions", json.dumps([{"Name": dimension, "Value": value}]),
            "--treat-missing-data", "notBreaching")
    print("Enabled 35-day recovery, deletion protection, and six CloudWatch alarms.", flush=True)
    print("Alarm states are visible in CloudWatch; notification subscriptions are not configured.", flush=True)


def check(protection=True):
    table = aws("dynamodb", "describe-table", "--table-name", CONFIG["table"])["Table"]
    require(table["TableStatus"] == "ACTIVE", "DynamoDB is not active")
    require(table["KeySchema"] == [{"AttributeName": "PK", "KeyType": "HASH"},
                                   {"AttributeName": "SK", "KeyType": "RANGE"}], "Unexpected table keys")
    require(any(i["IndexName"] == "bearer_token-index" and i["IndexStatus"] == "ACTIVE"
                and i["KeySchema"] == [{"AttributeName": "bearer_token", "KeyType": "HASH"}]
                and i["Projection"]["ProjectionType"] == "ALL"
                for i in table.get("GlobalSecondaryIndexes", [])), "Missing bearer token index")
    require(table.get("StreamSpecification") == {"StreamEnabled": True, "StreamViewType": "NEW_IMAGE"},
            "Expected NEW_IMAGE table stream")
    secret = aws("secretsmanager", "get-secret-value", "--secret-id", CONFIG["secret"])
    credentials = json.loads(secret["SecretString"])
    require(all(isinstance(credentials.get(k), str) and credentials[k].strip()
                for k in ("WHOOP_CLIENT_ID", "WHOOP_CLIENT_SECRET")), "WHOOP credentials are incomplete")
    queue_url = aws("sqs", "get-queue-url", "--queue-name", CONFIG["failure_queue"])["QueueUrl"]
    queue = aws("sqs", "get-queue-attributes", "--queue-url", queue_url,
                "--attribute-names", "All")["Attributes"]
    require(queue.get("SqsManagedSseEnabled") == "true" or queue.get("KmsMasterKeyId"), "Failure queue is not encrypted")
    print(f"Failure queue: {queue['ApproximateNumberOfMessages']} visible message(s)", flush=True)

    base = aws("apigatewayv2", "get-api", "--api-id", CONFIG["api_id"])["ApiEndpoint"]
    api = function(CONFIG["api_function"])
    sync = function(CONFIG["sync_function"])
    for cfg, timeout in ((api, 30), (sync, 60)):
        require(cfg["State"] == "Active" and cfg["LastUpdateStatus"] == "Successful", "Lambda update incomplete")
        require(cfg["Runtime"] == "provided.al2023" and cfg["Architectures"] == ["arm64"], "Unexpected Lambda runtime")
        require(cfg["Timeout"] == timeout and cfg["MemorySize"] == 256, "Unexpected Lambda sizing")
        require(cfg["Environment"]["Variables"].get("SECRET_NAME") == CONFIG["secret"], "Incorrect secret environment")
    require(api["Environment"]["Variables"].get("WHOOP_REDIRECT_URI") == base + "/auth/whoop/callback", "Incorrect redirect URI")
    require(api["Environment"]["Variables"].get("USER_TIMEZONE") == CONFIG["timezone"], "Incorrect timezone")
    routes = aws("apigatewayv2", "get-routes", "--api-id", CONFIG["api_id"])["Items"]
    integrations = aws("apigatewayv2", "get-integrations", "--api-id", CONFIG["api_id"])["Items"]
    valid_targets = {"integrations/" + i["IntegrationId"] for i in integrations
                     if i.get("PayloadFormatVersion") == "2.0" and i.get("IntegrationType") == "AWS_PROXY"
                     and i.get("IntegrationUri") == api["FunctionArn"]}
    for route in ROUTES:
        require(any(r["RouteKey"] == route and r.get("Target") in valid_targets for r in routes), "Missing route: " + route)
    stages = aws("apigatewayv2", "get-stages", "--api-id", CONFIG["api_id"])["Items"]
    require(any(s["StageName"] == "$default" and s.get("DeploymentId") for s in stages), "Missing deployed default stage")
    mappings = aws("lambda", "list-event-source-mappings", "--function-name", CONFIG["sync_function"])["EventSourceMappings"]
    mappings = [m for m in mappings if m["EventSourceArn"] == table["LatestStreamArn"]]
    require(len(mappings) == 1, "Expected exactly one stream mapping")
    mapping = mappings[0]
    require(mapping["State"] == "Enabled" and mapping.get("LastProcessingResult") == "OK", "Stream mapping is not healthy")
    require(mapping["BatchSize"] == 1 and mapping["ParallelizationFactor"] == 1
            and mapping["MaximumRetryAttempts"] == 3 and mapping["MaximumRecordAgeInSeconds"] == 3600,
            "Incorrect stream retry configuration")
    require([json.loads(f["Pattern"]) for f in mapping.get("FilterCriteria", {}).get("Filters", [])] == [FILTER], "Incorrect stream filter")
    require(mapping.get("DestinationConfig", {}).get("OnFailure", {}).get("Destination") == queue["QueueArn"], "Incorrect failure destination")

    # Simulate effective IAM access, including managed and inline policies.
    for cfg, permissions in ((api, {
            table["TableArn"]: ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Query"],
            table["TableArn"] + "/index/bearer_token-index": ["dynamodb:Query"],
            secret["ARN"]: ["secretsmanager:GetSecretValue"]}), (sync, {
            table["TableArn"]: ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Query", "dynamodb:DeleteItem"],
            table["LatestStreamArn"]: ["dynamodb:DescribeStream", "dynamodb:GetRecords", "dynamodb:GetShardIterator"],
            "*": ["dynamodb:ListStreams"], secret["ARN"]: ["secretsmanager:GetSecretValue"],
            queue["QueueArn"]: ["sqs:SendMessage"]})):
        for resource, actions in permissions.items():
            result = aws("iam", "simulate-principal-policy", "--policy-source-arn", cfg["Role"],
                         "--action-names", *actions, "--resource-arns", resource)
            require(all(r["EvalDecision"] == "allowed" for r in result["EvaluationResults"]),
                    "Missing IAM access for " + cfg["FunctionName"] + ": " + ", ".join(actions))
    if protection:
        require(table.get("DeletionProtectionEnabled"), "Table deletion protection is disabled; run protect")
        backups = aws("dynamodb", "describe-continuous-backups", "--table-name", CONFIG["table"])
        pitr = backups["ContinuousBackupsDescription"]["PointInTimeRecoveryDescription"]
        require(pitr["PointInTimeRecoveryStatus"] == "ENABLED" and pitr["RecoveryPeriodInDays"] == 35, "35-day recovery is disabled")
        found = aws("cloudwatch", "describe-alarms", "--alarm-names", *[a[0] for a in alarms()])["MetricAlarms"]
        require(len(found) == 6, "Missing CloudWatch alarms; run protect")
        for alarm in found:
            print(f"Alarm {alarm['AlarmName']}: {alarm['StateValue']}", flush=True)
    smoke(base)
    print("Backend configuration, IAM, stream, and HTTPS checks passed.", flush=True)
    return base


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def smoke(base):
    opener = urllib.request.build_opener(NoRedirect)
    for route, expected in [(ROUTES[0], 302), (ROUTES[1], 400), (ROUTES[2], 401), (ROUTES[3], 401), (ROUTES[4], 401)]:
        method, path = route.split(" ", 1)
        try:
            response = opener.open(urllib.request.Request(base + path, method=method), timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            require(response.status == expected, f"{route}: expected {expected}, got {response.status}")
            if expected == 302:
                redirect = urllib.parse.urlparse(response.headers.get("Location", ""))
                require(redirect.scheme == "https" and redirect.hostname == "api.prod.whoop.com", "Unexpected OAuth redirect")
                require(urllib.parse.parse_qs(redirect.query).get("redirect_uri") == [base + "/auth/whoop/callback"], "Incorrect OAuth callback")
                cookie = response.headers.get("Set-Cookie", "")
                require(all(flag in cookie for flag in ("Secure", "HttpOnly", "SameSite=Lax")), "Missing OAuth cookie protection")
        print(f"{route}: {expected}", flush=True)


def smoke_data():
    """Exercise authenticated writes and signed stream processing using disposable rows."""
    base = aws("apigatewayv2", "get-api", "--api-id", CONFIG["api_id"])["ApiEndpoint"]
    user_id = 2_000_000_000 + secrets.randbelow(100_000_000)
    user_pk, webhook_pk = f"USER#{user_id}", f"WHOOPUSER#{user_id}"
    token = secrets.token_hex(32)
    sleep_id = str(uuid.uuid4())

    def rows(pk):
        items, start = [], None
        while True:
            args = ["dynamodb", "query", "--table-name", CONFIG["table"], "--consistent-read",
                    "--key-condition-expression", "PK = :pk", "--expression-attribute-values",
                    json.dumps({":pk": {"S": pk}})]
            if start:
                args += ["--exclusive-start-key", json.dumps(start)]
            result = aws(*args)
            items.extend(result["Items"])
            start = result.get("LastEvaluatedKey")
            if not start:
                return items

    def put(pk, sk, **values):
        item = {"PK": {"S": pk}, "SK": {"S": sk}, **values}
        aws("dynamodb", "put-item", "--table-name", CONFIG["table"], "--item", json.dumps(item),
            "--condition-expression", "attribute_not_exists(PK)")

    def request(path, body=None, headers=None):
        req = urllib.request.Request(base + path, data=body, headers=headers or {},
                                     method="GET" if body is None else "POST")
        with urllib.request.urlopen(req, timeout=30) as response:
            require(response.status == 200, "Smoke request failed: " + path)
            return json.load(response)

    require(not rows(user_pk) and not rows(webhook_pk), "Smoke identity already exists; retry")
    created = False
    try:
        # Deletion webhooks do not call WHOOP, so no real OAuth tokens are used.
        put(user_pk, "PROFILE", whoop_user_id={"N": str(user_id)}, bearer_token={"S": token})
        created = True
        auth = {"Authorization": "Bearer " + token}
        for attempt in range(20):
            try:
                request("/last", headers=auth)
                break
            except urllib.error.HTTPError as error:
                if error.code != 401 or attempt == 19:
                    raise
                time.sleep(1)  # Allow the bearer-token GSI to catch up.
        result = request("/phone-lock", body=b"", headers=auth)
        require(result.get("status") == "recorded", "Phone event was not accepted")
        require(any(r["SK"]["S"].startswith("PHONELOCK#") for r in rows(user_pk)), "Phone event was not persisted")
        print("Authenticated /last and phone-lock persistence passed.", flush=True)
        put(user_pk, "SLEEP#" + sleep_id, data={"S": json.dumps({"id": sleep_id})})
        put(user_pk, "RECOVERY#" + sleep_id, data={"S": json.dumps({"sleep_id": sleep_id})})
        secret = json.loads(aws("secretsmanager", "get-secret-value", "--secret-id", CONFIG["secret"])["SecretString"])
        body = json.dumps({"user_id": user_id, "id": sleep_id, "type": "sleep.deleted",
                           "trace_id": str(uuid.uuid4())}).encode()
        timestamp = str(int(time.time() * 1000))
        signature = base64.b64encode(hmac.new(secret["WHOOP_CLIENT_SECRET"].encode(),
                                            timestamp.encode() + body, hashlib.sha256).digest()).decode()
        result = request("/webhook/whoop", body=body, headers={"Content-Type": "application/json",
                         "X-WHOOP-Signature-Timestamp": timestamp, "X-WHOOP-Signature": signature})
        require(result.get("status") == "ok", "Signed webhook was not accepted")
        for _ in range(30):
            current = rows(user_pk)
            if not any(r["SK"]["S"] in ("SLEEP#" + sleep_id, "RECOVERY#" + sleep_id) for r in current):
                print("Signed webhook -> API -> DynamoDB stream -> sync deletion passed.", flush=True)
                break
            time.sleep(1)
        else:
            raise RuntimeError("Stream worker did not delete smoke records within 30 polls")
        require(any(r["SK"]["S"].startswith("WEBHOOK#") for r in rows(webhook_pk)), "Webhook was not persisted")
    finally:
        if created:
            # Only the two unused partitions claimed above belong to this test.
            for pk in (user_pk, webhook_pk):
                for item in rows(pk):
                    aws("dynamodb", "delete-item", "--table-name", CONFIG["table"],
                        "--key", json.dumps({k: item[k] for k in ("PK", "SK")}))
            print("Temporary smoke records removed.", flush=True)


def deploy():
    check(protection=False)
    for command in (["go", "test", "-race", "./..."], ["go", "vet", "./..."],
                    ["make", "build-api", "build-sync"]):
        subprocess.run(command, cwd=ROOT, check=True)
    backup = ROOT / "bin/rollback" / datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    backup.mkdir(parents=True, mode=0o700)
    jobs = [(CONFIG["api_function"], ROOT / "bin/api/lambda-api.zip"),
            (CONFIG["sync_function"], ROOT / "bin/sync/lambda-sync.zip")]
    originals = {}
    for name, _ in jobs:
        original = aws("lambda", "get-function", "--function-name", name)
        path = backup / (name + ".zip")
        urllib.request.urlretrieve(original["Code"]["Location"], path)
        originals[name] = original["Configuration"]["RevisionId"]
    changed = []
    try:
        for name, path in jobs:
            result = aws("lambda", "update-function-code", "--function-name", name,
                         "--revision-id", originals[name], "--zip-file", "fileb://" + str(path), "--publish")
            changed.append(name)
            aws("lambda", "wait", "function-updated-v2", "--function-name", name)
            expected = base64.b64encode(hashlib.sha256(path.read_bytes()).digest()).decode()
            require(function(name)["CodeSha256"] == expected, "Deployed ZIP checksum mismatch")
            print(f"Deployed {name}, version {result['Version']}", flush=True)
        check()
        smoke_data()
    except Exception:
        for name in reversed(changed):
            try:
                aws("lambda", "wait", "function-updated-v2", "--function-name", name)
                aws("lambda", "update-function-code", "--function-name", name,
                    "--zip-file", "fileb://" + str(backup / (name + ".zip")))
                aws("lambda", "wait", "function-updated-v2", "--function-name", name)
                print("Restored " + name, file=sys.stderr)
            except Exception as error:
                print(f"Automatic rollback failed for {name}: {error}", file=sys.stderr)
        raise
    print(f"Previous Lambda packages saved in {backup.relative_to(ROOT)}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["check", "protect", "deploy", "smoke-data"])
    parser.add_argument("--profile", default=CONFIG["profile"])
    args = parser.parse_args()
    CONFIG["profile"] = args.profile
    identity()
    if args.command == "check":
        check()
    elif args.command == "protect":
        protect()
        check()
    elif args.command == "deploy":
        deploy()
    else:
        smoke_data()


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError, KeyError, ValueError, OSError) as error:
        print(f"Backend operation failed: {error}", file=sys.stderr)
        sys.exit(1)
