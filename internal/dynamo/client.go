package dynamo

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const tableName = "sleep-onset-events"

type Client struct {
	db *dynamodb.Client
}

func New(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &Client{
		db: dynamodb.NewFromConfig(cfg),
	}, nil
}

type PhoneLockEvent struct {
	PK       string    `dynamodbav:"PK"`
	SK       string    `dynamodbav:"SK"`
	LockedAt time.Time `dynamodbav:"locked_at"`
}

func (c *Client) PutPhoneLockEvent(ctx context.Context, userID string, lockedAt time.Time) error {
	event := PhoneLockEvent{
		PK:       userID,
		SK:       fmt.Sprintf("PHONELOCK#%s", lockedAt.Format(time.RFC3339)),
		LockedAt: lockedAt,
	}

	item, err := attributevalue.MarshalMap(event)
	if err != nil {
		return fmt.Errorf("failed to marshal phone-lock event: %w", err)
	}

	_, err = c.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})

	return err
}

func (c *Client) GetLatestPhoneLockEvent(ctx context.Context, userPK string) (*PhoneLockEvent, error) {
	result, err := c.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: userPK},
			":prefix": &types.AttributeValueMemberS{Value: "PHONELOCK#"},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query latest phone lock: %w", err)
	}
	if len(result.Items) == 0 {
		return nil, nil
	}

	var event PhoneLockEvent
	if err := attributevalue.UnmarshalMap(result.Items[0], &event); err != nil {
		return nil, fmt.Errorf("failed to unmarshal phone lock event: %w", err)
	}
	return &event, nil
}

type User struct {
	PK           string    `dynamodbav:"PK"`
	SK           string    `dynamodbav:"SK"`
	WhoopUserID  int       `dynamodbav:"whoop_user_id"`
	AccessToken  string    `dynamodbav:"access_token"`
	RefreshToken string    `dynamodbav:"refresh_token"`
	TokenExpiry  time.Time `dynamodbav:"token_expiry"`
	BearerToken  string    `dynamodbav:"bearer_token"`
}

func (c *Client) PutUser(ctx context.Context, user *User) error {
	item, err := attributevalue.MarshalMap(user)
	if err != nil {
		return fmt.Errorf("failed to marshal user: %w", err)
	}

	_, err = c.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})

	return err
}

func (c *Client) GetUserByWhoopID(ctx context.Context, whoopUserID int) (*User, error) {
	pk := fmt.Sprintf("USER#%d", whoopUserID)
	result, err := c.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: "PROFILE"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get user by whoop ID: %w", err)
	}
	if result.Item == nil {
		return nil, fmt.Errorf("no user found for whoop ID %d", whoopUserID)
	}

	var user User
	if err := attributevalue.UnmarshalMap(result.Item, &user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user: %w", err)
	}
	return &user, nil
}

func (c *Client) GetUserByBearerToken(ctx context.Context, token string) (*User, error) {
	result, err := c.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		IndexName:              aws.String("bearer_token-index"),
		KeyConditionExpression: aws.String("bearer_token = :token"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":token": &types.AttributeValueMemberS{Value: token},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query by bearer token: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("no user found for token")
	}

	var user User
	if err := attributevalue.UnmarshalMap(result.Items[0], &user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user: %w", err)
	}

	return &user, nil
}

type WebhookEvent struct {
	PK        string    `dynamodbav:"PK"`
	SK        string    `dynamodbav:"SK"`
	Type      string    `dynamodbav:"type"`
	WhoopID   string    `dynamodbav:"whoop_id"`
	TraceID   string    `dynamodbav:"trace_id"`
	Timestamp time.Time `dynamodbav:"timestamp"`
}

func (c *Client) PutWebhookEvent(ctx context.Context, whoopUserID int, eventType, whoopID, traceID string) error {
	now := time.Now().UTC()
	event := WebhookEvent{
		PK:        fmt.Sprintf("WHOOPUSER#%d", whoopUserID),
		SK:        fmt.Sprintf("WEBHOOK#%s#%s", eventType, now.Format(time.RFC3339)),
		Type:      eventType,
		WhoopID:   whoopID,
		TraceID:   traceID,
		Timestamp: now,
	}

	item, err := attributevalue.MarshalMap(event)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook event: %w", err)
	}

	_, err = c.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})

	return err
}

type SyncRecord struct {
	PK        string    `dynamodbav:"PK"`
	SK        string    `dynamodbav:"SK"`
	Data      string    `dynamodbav:"data"`
	SyncedAt  time.Time `dynamodbav:"synced_at"`
}

func (c *Client) PutSyncRecord(ctx context.Context, userPK, sk, data string) error {
	record := SyncRecord{
		PK:       userPK,
		SK:       sk,
		Data:     data,
		SyncedAt: time.Now().UTC(),
	}

	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return fmt.Errorf("failed to marshal sync record: %w", err)
	}

	_, err = c.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})

	return err
}

// GetLatestSyncRecord queries for the most recent record matching a SK prefix (e.g. "SLEEP#", "RECOVERY#").
func (c *Client) GetLatestSyncRecord(ctx context.Context, userPK, skPrefix string) (*SyncRecord, error) {
	result, err := c.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: userPK},
			":prefix": &types.AttributeValueMemberS{Value: skPrefix},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query latest %s: %w", skPrefix, err)
	}

	if len(result.Items) == 0 {
		return nil, nil
	}

	var record SyncRecord
	if err := attributevalue.UnmarshalMap(result.Items[0], &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal sync record: %w", err)
	}

	return &record, nil
}

func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var users []User
	var lastKey map[string]types.AttributeValue

	for {
		input := &dynamodb.ScanInput{
			TableName:        aws.String(tableName),
			FilterExpression: aws.String("SK = :sk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":sk": &types.AttributeValueMemberS{Value: "PROFILE"},
			},
		}
		if lastKey != nil {
			input.ExclusiveStartKey = lastKey
		}

		result, err := c.db.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to scan users: %w", err)
		}

		var page []User
		if err := attributevalue.UnmarshalListOfMaps(result.Items, &page); err != nil {
			return nil, fmt.Errorf("failed to unmarshal users: %w", err)
		}
		users = append(users, page...)

		if result.LastEvaluatedKey == nil {
			break
		}
		lastKey = result.LastEvaluatedKey
	}

	return users, nil
}

func (c *Client) GetUser(ctx context.Context, userID string) (*User, error) {
	result, err := c.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: fmt.Sprintf("USER#%s", userID)},
			"SK": &types.AttributeValueMemberS{Value: "PROFILE"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if result.Item == nil {
		return nil, fmt.Errorf("user not found: %s", userID)
	}

	var user User
	if err := attributevalue.UnmarshalMap(result.Item, &user); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user: %w", err)
	}

	return &user, nil
}
