package handlers

import (
	"context"
	"log/slog"
)

type ContextHandler struct {
	slog.Handler
	Keys []any
}

func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(h.observe(ctx)...)
	return h.Handler.Handle(ctx, r)
}

func (h ContextHandler) observe(ctx context.Context) (as []slog.Attr) {
	for _, k := range h.Keys {
		a, ok := ctx.Value(k).(slog.Attr)
		if !ok {
			continue
		}
		a.Value = a.Value.Resolve()
		as = append(as, a)
	}
	return
}
