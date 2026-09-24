package chat

import (
	"context"
	"fmt"

	chatapi "google.golang.org/api/chat/v1"
	"google.golang.org/api/option"
)

// New returns a Chat API client configured with opts.
func New(ctx context.Context, opts ...option.ClientOption) (*chatapi.Service, error) {
	svc, err := chatapi.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create chat client: %w", err)
	}
	return svc, nil
}
