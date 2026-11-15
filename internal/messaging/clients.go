package messaging

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func NewClients(ctx context.Context, region, endpoint string) (*sqs.Client, *sns.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, nil, fmt.Errorf("load aws config: %w", err)
	}

	sqsClient := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	snsClient := sns.NewFromConfig(cfg, func(o *sns.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return sqsClient, snsClient, nil
}
