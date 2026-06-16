package dynamobrain

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestNormalizeDynamoConfigTrimsStaticCredentials(t *testing.T) {
	cfg, err := normalizeDynamoConfig(brainConfig{
		TableName:       " atlas-brain ",
		Region:          " us-east-2 ",
		AccessKeyID:     " AKIAIOSFODNN7EXAMPLE ",
		SecretAccessKey: " wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY ",
	})
	if err != nil {
		t.Fatalf("normalizeDynamoConfig() error = %v", err)
	}
	if cfg.TableName != "atlas-brain" {
		t.Fatalf("TableName = %q, want atlas-brain", cfg.TableName)
	}
	if cfg.Region != "us-east-2" {
		t.Fatalf("Region = %q, want us-east-2", cfg.Region)
	}
	if cfg.AccessKeyID != "AKIAIOSFODNN7EXAMPLE" {
		t.Fatalf("AccessKeyID was not trimmed")
	}
	if cfg.SecretAccessKey != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
		t.Fatalf("SecretAccessKey was not trimmed")
	}
}

func TestNormalizeDynamoConfigRejectsIncompleteStaticCredentials(t *testing.T) {
	_, err := normalizeDynamoConfig(brainConfig{
		TableName:       "atlas-brain",
		Region:          "us-east-2",
		SecretAccessKey: "secret",
	})
	if err == nil || !strings.Contains(err.Error(), "AccessKeyID is empty") {
		t.Fatalf("normalizeDynamoConfig() error = %v, want missing AccessKeyID", err)
	}
}

func TestValidateDynamoAWSCredentialsRejectsHeaderBreakingAccessKeyID(t *testing.T) {
	err := validateDynamoAWSCredentials(aws.Credentials{
		AccessKeyID:     "not/an/access/key",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Source:          "EnvConfigCredentials",
	})
	if err == nil {
		t.Fatal("validateDynamoAWSCredentials() succeeded for malformed access key ID")
	}
	if got := err.Error(); !strings.Contains(got, "EnvConfigCredentials") ||
		!strings.Contains(got, "malformed") ||
		!strings.Contains(got, "AWS_ACCESS_KEY_ID") {
		t.Fatalf("validateDynamoAWSCredentials() error = %q, want actionable malformed credential hint", got)
	}
}

func TestValidateDynamoAWSCredentialsAllowsNormalTemporaryCredentials(t *testing.T) {
	err := validateDynamoAWSCredentials(aws.Credentials{
		AccessKeyID:     "ASIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    "temporary/session/token",
		Source:          "SharedConfigCredentials",
	})
	if err != nil {
		t.Fatalf("validateDynamoAWSCredentials() error = %v", err)
	}
}

func TestDynamoListMetadataScanInputAliasesReservedAttributeNames(t *testing.T) {
	input := dynamoListMetadataScanInput("atlas-brain")
	if input.ProjectionExpression == nil {
		t.Fatal("ProjectionExpression is nil")
	}
	if got := *input.ProjectionExpression; strings.Contains(got, "Format") || !strings.Contains(got, "#format") {
		t.Fatalf("ProjectionExpression = %q, want aliased Format attribute", got)
	}
	if got := input.ExpressionAttributeNames["#format"]; got != "Format" {
		t.Fatalf("ExpressionAttributeNames[#format] = %q, want Format", got)
	}
	if got := input.ExpressionAttributeNames["#memory"]; got != "Memory" {
		t.Fatalf("ExpressionAttributeNames[#memory] = %q, want Memory", got)
	}
}

func TestDynamoListKeysScanInputAliasesMemoryAttributeName(t *testing.T) {
	input := dynamoListKeysScanInput("atlas-brain")
	if input.ProjectionExpression == nil || *input.ProjectionExpression != "#memory" {
		t.Fatalf("ProjectionExpression = %v, want #memory", input.ProjectionExpression)
	}
	if got := input.ExpressionAttributeNames["#memory"]; got != "Memory" {
		t.Fatalf("ExpressionAttributeNames[#memory] = %q, want Memory", got)
	}
}
