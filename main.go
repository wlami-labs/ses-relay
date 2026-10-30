// ses-relay accepts SMTP on loopback and sends each message through the
// Amazon SES v2 API. Listmonk only speaks SMTP; this lets it use SES with
// short-lived credentials from the pod's projected service-account token
// (AWS_ROLE_ARN + AWS_WEB_IDENTITY_TOKEN_FILE) instead of SMTP passwords.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

const maxMessageBytes = 10 << 20

func main() {
	healthcheck := flag.Bool("healthcheck", false, "exit 0 if the relay accepts connections")
	flag.Parse()

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:2525"
	}
	if *healthcheck {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			os.Exit(1)
		}
		c.Close()
		return
	}

	configSet := os.Getenv("SES_CONFIGURATION_SET")
	if configSet == "" {
		slog.Error("SES_CONFIGURATION_SET is required")
		os.Exit(1)
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		slog.Error("aws config", "err", err)
		os.Exit(1)
	}
	sender := &sesSender{client: sesv2.NewFromConfig(cfg), configurationSet: configSet}

	slog.Info("listening", "addr", addr, "configuration_set", configSet, "region", cfg.Region)
	if err := NewServer(addr, sender, maxMessageBytes).ListenAndServe(); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
