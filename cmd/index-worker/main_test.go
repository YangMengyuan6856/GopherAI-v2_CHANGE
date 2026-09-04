package main

import (
	"GopherAI/config"
	"strings"
	"testing"

	"github.com/streadway/amqp"
)

func TestBuildRabbitURLEscapesCredentialsAndPreservesDefaultVhost(t *testing.T) {
	configuration := &config.Config{}
	configuration.RabbitmqHost = "rabbitmq"
	configuration.RabbitmqPort = 5672
	configuration.RabbitmqUsername = "worker@name"
	configuration.RabbitmqPassword = "secret:/value"
	configuration.RabbitmqVhost = "/"
	connectionURL := buildRabbitURL(configuration)
	if strings.Contains(connectionURL, "worker@name") || strings.Contains(connectionURL, "secret:/value") {
		t.Fatalf("credentials were not URL encoded: %s", connectionURL)
	}
	parsed, err := amqp.ParseURI(connectionURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Username != "worker@name" || parsed.Password != "secret:/value" || parsed.Vhost != "/" || parsed.Host != "rabbitmq" || parsed.Port != 5672 {
		t.Fatalf("unexpected parsed AMQP URL: %+v", parsed)
	}
}
