package main

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

type sesSender struct {
	client           *sesv2.Client
	configurationSet string
}

// SendRaw passes the message through untouched (List-Unsubscribe headers
// included); the envelope recipients become the destination, so Bcc works.
func (s *sesSender) SendRaw(ctx context.Context, from string, to []string, raw []byte) (string, error) {
	out, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress:     aws.String(from),
		Destination:          &types.Destination{ToAddresses: to},
		Content:              &types.EmailContent{Raw: &types.RawMessage{Data: raw}},
		ConfigurationSetName: aws.String(s.configurationSet),
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.MessageId), nil
}
