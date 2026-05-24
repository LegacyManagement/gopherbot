// Package dynamobrain is a simple AWS DynamoDB implementation of the bot.SimpleBrain
// interface, which gives the robot a place to permanently store it's memories.
package dynamobrain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/lnxjedi/gopherbot/robot"
)

var handler robot.Handler
var svc *dynamodb.Client

type brainConfig struct {
	TableName, Region, AccessKeyID, SecretAccessKey string
}

type dynaMemory struct {
	Memory    string
	Content   []byte
	Format    string
	Version   uint64
	Checksum  string
	Deleted   bool
	UpdatedAt string
}

var dynamocfg brainConfig

func (db *brainConfig) Store(k string, b *[]byte) error {
	item, err := attributevalue.MarshalMap(dynaMemory{
		Memory:  k,
		Content: *b,
	})
	if err != nil {
		handler.Log(robot.Error, "Error storing memory: %v", err)
		return err
	}

	input := &dynamodb.PutItemInput{
		Item:      item,
		TableName: aws.String(dynamocfg.TableName),
	}

	_, err = svc.PutItem(context.Background(), input)
	if err != nil {
		logDynamoError("storing memory", err)
		return err
	}

	return nil
}

func (db *brainConfig) Retrieve(k string) (datum *[]byte, exists bool, err error) {
	consistent := true
	result, err := svc.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName:      aws.String(dynamocfg.TableName),
		Key:            map[string]types.AttributeValue{"Memory": &types.AttributeValueMemberS{Value: k}},
		ConsistentRead: &consistent,
	})

	if err != nil {
		logDynamoError("retrieving memory", err)
		return nil, false, err
	}

	m := dynaMemory{}

	err = attributevalue.UnmarshalMap(result.Item, &m)

	if err != nil {
		handler.Log(robot.Error, "Failed to unmarshal Record, %v", err)
		return nil, false, err
	}

	if m.Memory == "" {
		return nil, false, nil
	}

	return &m.Content, true, nil
}

func (db *brainConfig) Delete(key string) error {
	delete := &dynamodb.DeleteItemInput{
		Key:       map[string]types.AttributeValue{"Memory": &types.AttributeValueMemberS{Value: key}},
		TableName: aws.String(dynamocfg.TableName),
	}
	_, err := svc.DeleteItem(context.Background(), delete)
	return err
}

func (db *brainConfig) List() ([]string, error) {
	keys := make([]string, 0)
	keyName := "Memory"
	scan := &dynamodb.ScanInput{
		ProjectionExpression: &keyName,
		TableName:            aws.String(dynamocfg.TableName),
	}
	res, err := svc.Scan(context.Background(), scan)
	if err != nil {
		return keys, err
	}
	for _, av := range res.Items {
		for _, item := range av {
			var m string
			err := attributevalue.Unmarshal(item, &m)
			if err != nil {
				return keys, err
			}
			keys = append(keys, m)
		}
	}
	return keys, nil
}

func (db *brainConfig) Shutdown() {
	// nothing to do, everything is synchronous
}

func provider(r robot.Handler) robot.SimpleBrain {
	remoteProvider(r)
	return &dynamocfg
}

func remoteProvider(r robot.Handler) robot.RemoteBrainBackend {
	handler = r
	handler.GetBrainConfig(&dynamocfg)
	ctx := context.Background()
	accessKeyID := dynamocfg.AccessKeyID
	secretAccessKey := dynamocfg.SecretAccessKey
	var cfg aws.Config
	var err error
	if len(accessKeyID) == 0 {
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(dynamocfg.Region))
		if err != nil {
			handler.Log(robot.Fatal, "Unable to establish AWS session: %v", err)
		}
	} else {
		creds := credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(dynamocfg.Region), config.WithCredentialsProvider(creds))
		if err != nil {
			handler.Log(robot.Fatal, "Unable to establish AWS session: %v", err)
		}
	}
	// Create DynamoDB client
	svc = dynamodb.NewFromConfig(cfg)
	input := &dynamodb.DescribeTableInput{
		TableName: aws.String(dynamocfg.TableName),
	}
	_, err = svc.DescribeTable(ctx, input)
	if err != nil {
		logDynamoError("describing table", err)
		handler.Log(robot.Fatal, "Error describing table '%s': %v", dynamocfg.TableName, err)
	}

	return &dynamoRemoteBrain{cfg: dynamocfg}
}

type dynamoRemoteBrain struct {
	cfg brainConfig
}

func (db *dynamoRemoteBrain) Identity() robot.BrainBackendIdentity {
	return robot.BrainBackendIdentity{Provider: "dynamo", Scope: db.cfg.Region + "/" + db.cfg.TableName}
}

func (db *dynamoRemoteBrain) SyncPolicy() robot.BrainSyncPolicy {
	return robot.BrainSyncPolicy{}
}

func (db *dynamoRemoteBrain) Get(ctx context.Context, key string) (robot.RemoteBrainRecord, bool, error) {
	consistent := true
	result, err := svc.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(dynamocfg.TableName),
		Key:            map[string]types.AttributeValue{"Memory": &types.AttributeValueMemberS{Value: key}},
		ConsistentRead: &consistent,
	})
	if err != nil {
		logDynamoError("retrieving memory", err)
		return robot.RemoteBrainRecord{}, false, err
	}
	if len(result.Item) == 0 {
		return robot.RemoteBrainRecord{}, false, nil
	}
	m := dynaMemory{}
	if err := attributevalue.UnmarshalMap(result.Item, &m); err != nil {
		return robot.RemoteBrainRecord{}, false, err
	}
	if m.Memory == "" {
		return robot.RemoteBrainRecord{}, false, nil
	}
	if m.Format != brainCacheFormat {
		return robot.RemoteBrainRecord{Key: key}, true, fmt.Errorf("not a v3 brain record")
	}
	updatedAt, _ := time.Parse(time.RFC3339Nano, m.UpdatedAt)
	return robot.RemoteBrainRecord{
		Key:       m.Memory,
		Payload:   m.Content,
		Format:    m.Format,
		Version:   m.Version,
		Checksum:  m.Checksum,
		Deleted:   m.Deleted,
		UpdatedAt: updatedAt,
	}, true, nil
}

func (db *dynamoRemoteBrain) Put(ctx context.Context, record robot.RemoteBrainRecord) error {
	item, err := attributevalue.MarshalMap(dynaMemory{
		Memory:    record.Key,
		Content:   record.Payload,
		Format:    brainCacheFormat,
		Version:   record.Version,
		Checksum:  record.Checksum,
		Deleted:   record.Deleted,
		UpdatedAt: record.UpdatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	_, err = svc.PutItem(ctx, &dynamodb.PutItemInput{Item: item, TableName: aws.String(dynamocfg.TableName)})
	return err
}

func (db *dynamoRemoteBrain) Delete(ctx context.Context, tombstone robot.RemoteBrainRecord) error {
	tombstone.Format = brainCacheFormat
	tombstone.Deleted = true
	return db.Put(ctx, tombstone)
}

func (db *dynamoRemoteBrain) ListMetadata(ctx context.Context, cursor string, limit int) (robot.RemoteBrainPage, error) {
	expr := "Memory, Format, Version, Checksum, Deleted, UpdatedAt"
	input := &dynamodb.ScanInput{
		ProjectionExpression: &expr,
		TableName:            aws.String(dynamocfg.TableName),
	}
	res, err := svc.Scan(ctx, input)
	if err != nil {
		return robot.RemoteBrainPage{}, err
	}
	records := make([]robot.RemoteBrainRecord, 0, len(res.Items))
	for _, item := range res.Items {
		var m dynaMemory
		if err := attributevalue.UnmarshalMap(item, &m); err != nil {
			return robot.RemoteBrainPage{}, err
		}
		updatedAt, _ := time.Parse(time.RFC3339Nano, m.UpdatedAt)
		records = append(records, robot.RemoteBrainRecord{
			Key:       m.Memory,
			Format:    m.Format,
			Version:   m.Version,
			Checksum:  m.Checksum,
			Deleted:   m.Deleted,
			UpdatedAt: updatedAt,
		})
	}
	return robot.RemoteBrainPage{Records: records}, nil
}

func (db *dynamoRemoteBrain) Shutdown() {}

func (db *dynamoRemoteBrain) ListV2(ctx context.Context, cursor string, limit int) (robot.LegacyBrainPage, error) {
	keys, err := db.cfg.List()
	if err != nil {
		return robot.LegacyBrainPage{}, err
	}
	records := make([]robot.LegacyBrainRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, robot.LegacyBrainRecord{Key: key})
	}
	return robot.LegacyBrainPage{Records: records}, nil
}

func (db *dynamoRemoteBrain) GetV2(ctx context.Context, key string) (robot.LegacyBrainRecord, bool, error) {
	payload, exists, err := db.cfg.Retrieve(key)
	if err != nil || !exists || payload == nil {
		return robot.LegacyBrainRecord{}, exists, err
	}
	return robot.LegacyBrainRecord{Key: key, Payload: *payload}, true, nil
}

func (db *dynamoRemoteBrain) PutV2(ctx context.Context, record robot.LegacyBrainRecord) error {
	payload := record.Payload
	return db.cfg.Store(record.Key, &payload)
}

func (db *dynamoRemoteBrain) DeleteV2(ctx context.Context, key string) error {
	return db.cfg.Delete(key)
}

const brainCacheFormat = "gopherbot-brain-v3"

func logDynamoError(action string, err error) {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		handler.Log(robot.Error, "Error %s: %s, %s", action, apiErr.ErrorCode(), apiErr.ErrorMessage())
		return
	}

	var resourceNotFound *types.ResourceNotFoundException
	if errors.As(err, &resourceNotFound) {
		handler.Log(robot.Error, "Error %s: %v", action, resourceNotFound)
		return
	}
	var throughput *types.ProvisionedThroughputExceededException
	if errors.As(err, &throughput) {
		handler.Log(robot.Error, "Error %s: %v", action, throughput)
		return
	}
	var internal *types.InternalServerError
	if errors.As(err, &internal) {
		handler.Log(robot.Error, "Error %s: %v", action, internal)
		return
	}
	var itemSize *types.ItemCollectionSizeLimitExceededException
	if errors.As(err, &itemSize) {
		handler.Log(robot.Error, "Error %s: %v", action, itemSize)
		return
	}
	var cond *types.ConditionalCheckFailedException
	if errors.As(err, &cond) {
		handler.Log(robot.Error, "Error %s: %v", action, cond)
		return
	}

	handler.Log(robot.Error, "Error %s: %v", action, err)
}
