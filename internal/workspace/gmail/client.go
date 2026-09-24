package gmail

import (
	"context"
	"fmt"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// New returns a Gmail API client configured with opts.
func New(ctx context.Context, opts ...option.ClientOption) (*gmailapi.Service, error) {
	svc, err := gmailapi.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create gmail client: %w", err)
	}
	return svc, nil
}
