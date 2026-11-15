package config

import (
	"strings"

	"github.com/spf13/viper"
)

func Load() (*Config, error) {
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetDefault("DATABASE_MAX_CONNECTIONS", 5)
	viper.SetDefault("SERVER_PORT", "8080")
	viper.SetDefault("AWS_DEFAULT_REGION", "sa-east-1")
	viper.SetDefault("MESSAGING_POLL_WAIT", "20s")
	viper.SetDefault("OUTBOX_POLL_INTERVAL", "500ms")
	viper.SetDefault("SAGA_TICK", "1s")
	viper.SetDefault("SAGA_COMMAND_TIMEOUT", "30s")
	viper.SetDefault("SAGA_MAX_ATTEMPTS", 5)
	viper.SetDefault("SAGA_APPROVAL_TIMEOUT", "168h")
	viper.SetDefault("SAGA_PAYMENT_TIMEOUT", "48h")
	viper.SetDefault("DEFAULT_BRANCH", "MATRIZ")

	config := Config{
		Server: &ServerConfig{
			Environment: viper.GetString("SERVER_ENVIRONMENT"),
			Port:        viper.GetString("SERVER_PORT"),
			BaseURL:     viper.GetString("APP_BASE_URL"),
		},
		Database: &DatabaseConfig{
			User:           viper.GetString("DATABASE_USER"),
			Password:       viper.GetString("DATABASE_PASSWORD"),
			Host:           viper.GetString("DATABASE_HOST"),
			Port:           viper.GetString("DATABASE_PORT"),
			Name:           viper.GetString("DATABASE_NAME"),
			MaxConnections: int32(viper.GetInt("DATABASE_MAX_CONNECTIONS")),
		},
		Cache: &CacheConfig{
			User:     viper.GetString("CACHE_USER"),
			Password: viper.GetString("CACHE_PASSWORD"),
			Host:     viper.GetString("CACHE_HOST"),
			Port:     viper.GetString("CACHE_PORT"),
		},
		JWT: &JWTConfig{
			SecretKey: viper.GetString("JWT_SECRET_KEY"),
		},
		AWS: &AWSConfig{
			DefaultRegion:  viper.GetString("AWS_DEFAULT_REGION"),
			SESSenderEmail: viper.GetString("SES_SENDER_EMAIL"),
			SESReplyTo:     viper.GetString("SES_REPLY_TO"),
			SESConfigSet:   viper.GetString("SES_CONFIG_SET"),
		},
		Email: &EmailConfig{
			Provider: viper.GetString("EMAIL_PROVIDER"),
			Host:     viper.GetString("EMAIL_HOST"),
			Port:     viper.GetInt("EMAIL_PORT"),
			From:     viper.GetString("EMAIL_FROM"),
		},
		Messaging: &MessagingConfig{
			Endpoint:           viper.GetString("AWS_ENDPOINT_URL"),
			Region:             viper.GetString("AWS_DEFAULT_REGION"),
			InboxQueueURL:      viper.GetString("MESSAGING_INBOX_QUEUE_URL"),
			BillingInboxURL:    viper.GetString("BILLING_INBOX_QUEUE_URL"),
			ExecutionInboxURL:  viper.GetString("EXECUTION_INBOX_QUEUE_URL"),
			PollWait:           viper.GetDuration("MESSAGING_POLL_WAIT"),
			OutboxPollInterval: viper.GetDuration("OUTBOX_POLL_INTERVAL"),
		},
		Internal: &InternalConfig{
			ServiceToken: viper.GetString("INTERNAL_SERVICE_TOKEN"),
		},
		Saga: &SagaConfig{
			Tick:            viper.GetDuration("SAGA_TICK"),
			CommandTimeout:  viper.GetDuration("SAGA_COMMAND_TIMEOUT"),
			MaxAttempts:     viper.GetInt("SAGA_MAX_ATTEMPTS"),
			ApprovalTimeout: viper.GetDuration("SAGA_APPROVAL_TIMEOUT"),
			PaymentTimeout:  viper.GetDuration("SAGA_PAYMENT_TIMEOUT"),
			DefaultBranch:   viper.GetString("DEFAULT_BRANCH"),
		},
	}

	return &config, nil
}
