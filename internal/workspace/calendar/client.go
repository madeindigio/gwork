package calendar

import (
	"context"
	"fmt"

	calendarapi "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// New returns a Calendar API client configured with opts.
func New(ctx context.Context, opts ...option.ClientOption) (*calendarapi.Service, error) {
	svc, err := calendarapi.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create calendar client: %w", err)
	}
	return svc, nil
}
