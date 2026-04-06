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
