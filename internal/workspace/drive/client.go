package drive

import (
	"context"
	"fmt"

	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// New returns a Drive API client configured with opts.
func New(ctx context.Context, opts ...option.ClientOption) (*driveapi.Service, error) {
	svc, err := driveapi.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create drive client: %w", err)
	}
	return svc, nil
}
