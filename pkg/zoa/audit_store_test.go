package zoa

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamoAuditStore_Record(t *testing.T) {
	var capturedInput *dynamodb.PutItemInput
	client := &mockDynamoClient{
		putItemFunc: func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			capturedInput = params
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 365)

	entry := &AuditEntry{
		AccountID:  "111222333444",
		CallerARN:  "arn:aws:iam::111222333444:user/slopezma",
		Operator:   "slopezma",
		Method:     "POST",
		Path:       "get_nodes/run",
		Action:     "get_nodes",
		StatusCode: 202,
	}

	err := store.Record(context.Background(), entry)
	require.NoError(t, err)
	assert.Equal(t, "audit-table", *capturedInput.TableName)
	assert.NotEmpty(t, entry.ID)
	assert.NotEmpty(t, entry.Timestamp)
	assert.NotZero(t, entry.TTL)
}

func TestDynamoAuditStore_Record_NanosecondPrecision(t *testing.T) {
	var timestamps []string
	client := &mockDynamoClient{
		putItemFunc: func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 365)

	entry1 := &AuditEntry{AccountID: "111", Method: "POST", StatusCode: 202}
	entry2 := &AuditEntry{AccountID: "111", Method: "GET", StatusCode: 200}

	_ = store.Record(context.Background(), entry1)
	timestamps = append(timestamps, entry1.Timestamp)
	time.Sleep(time.Millisecond)
	_ = store.Record(context.Background(), entry2)
	timestamps = append(timestamps, entry2.Timestamp)

	assert.NotEqual(t, timestamps[0], timestamps[1], "nanosecond timestamps must differ to prevent sort key collisions")
	assert.Len(t, timestamps[0], len("2006-01-02T15:04:05.000000000Z"))
}

func TestDynamoAuditStore_Record_PreservesExistingTimestamp(t *testing.T) {
	client := &mockDynamoClient{
		putItemFunc: func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 365)

	customTS := "2026-01-01T00:00:00.123456789Z"
	entry := &AuditEntry{
		AccountID:  "111",
		Method:     "GET",
		StatusCode: 200,
		Timestamp:  customTS,
	}

	err := store.Record(context.Background(), entry)
	require.NoError(t, err)
	assert.Equal(t, customTS, entry.Timestamp)
}

func TestDynamoAuditStore_Record_CustomTTL(t *testing.T) {
	client := &mockDynamoClient{
		putItemFunc: func(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
			return &dynamodb.PutItemOutput{}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 30)

	entry := &AuditEntry{AccountID: "111", Method: "POST", StatusCode: 202}
	err := store.Record(context.Background(), entry)
	require.NoError(t, err)

	expectedMin := time.Now().UTC().AddDate(0, 0, 29).Unix()
	expectedMax := time.Now().UTC().AddDate(0, 0, 31).Unix()
	assert.Greater(t, entry.TTL, expectedMin)
	assert.Less(t, entry.TTL, expectedMax)
}

func TestDynamoAuditStore_List(t *testing.T) {
	client := &mockDynamoClient{
		queryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			assert.Equal(t, "audit-table", *params.TableName)
			assert.Contains(t, *params.KeyConditionExpression, "#aid = :aid")
			assert.False(t, *params.ScanIndexForward)

			item1, _ := attributevalue.MarshalMap(&AuditEntry{
				ID: "a1", AccountID: "111", Method: "POST", Action: "get_nodes",
				StatusCode: 202, Timestamp: "2026-01-01T10:00:00.000000000Z",
			})
			item2, _ := attributevalue.MarshalMap(&AuditEntry{
				ID: "a2", AccountID: "111", Method: "GET", Action: "",
				StatusCode: 200, Timestamp: "2026-01-01T10:00:01.000000000Z",
			})
			return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{item1, item2}}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 365)

	entries, err := store.List(context.Background(), "111", 50, nil)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, "a1", entries[0].ID)
	assert.Equal(t, "POST", entries[0].Method)
}

func TestDynamoAuditStore_List_WithMethodFilter(t *testing.T) {
	client := &mockDynamoClient{
		queryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			assert.NotNil(t, params.FilterExpression)
			assert.Contains(t, *params.FilterExpression, "#mth = :mth")
			return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{}}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 365)

	filter := &AuditFilter{Method: "POST"}
	entries, err := store.List(context.Background(), "111", 50, filter)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestDynamoAuditStore_List_WithSinceKeyCondition(t *testing.T) {
	client := &mockDynamoClient{
		queryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			assert.Contains(t, *params.KeyConditionExpression, "AND #ts >= :since")
			return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{}}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 365)

	filter := &AuditFilter{Since: "2026-01-01T00:00:00.000000000Z"}
	_, err := store.List(context.Background(), "111", 50, filter)
	require.NoError(t, err)
}

func TestDynamoAuditStore_List_WithMultipleFilters(t *testing.T) {
	client := &mockDynamoClient{
		queryFunc: func(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
			assert.NotNil(t, params.FilterExpression)
			assert.Contains(t, *params.FilterExpression, "#act = :act")
			assert.Contains(t, *params.FilterExpression, "#op = :op")
			assert.Contains(t, *params.FilterExpression, "AND")
			return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{}}, nil
		},
	}

	store := NewDynamoAuditStore("audit-table", client, testLogger(), 365)

	filter := &AuditFilter{Action: "get_nodes", Operator: "slopezma"}
	_, err := store.List(context.Background(), "111", 50, filter)
	require.NoError(t, err)
}

func TestNewDynamoAuditStore_DefaultTTL(t *testing.T) {
	store := NewDynamoAuditStore("t", &mockDynamoClient{}, testLogger(), 0)
	assert.Equal(t, 365, store.ttlDays)

	store2 := NewDynamoAuditStore("t", &mockDynamoClient{}, testLogger(), -1)
	assert.Equal(t, 365, store2.ttlDays)
}
