package clock

import (
	"context"
	"time"
)

type timeKey struct{}

func WithTime(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, timeKey{}, at)
}
func ContextTime(ctx context.Context) (time.Time, bool) {
	at, ok := ctx.Value(timeKey{}).(time.Time)
	return at, ok
}
