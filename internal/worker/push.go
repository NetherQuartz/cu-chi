package worker

import (
	"context"
	"encoding/json"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/push"
	"github.com/NetherQuartz/cu-chi/internal/store"
	webpush "github.com/SherClockHolmes/webpush-go"
	"net/http"
	"time"
)

// Push has an independent durable queue so a long SSH deployment cannot delay alerts.
type PushWorker struct {
	DB     *store.Store
	Box    *cryptobox.Box
	Origin string
	Client *http.Client
}

func (w *PushWorker) Run(ctx context.Context) {
	if w.Client == nil {
		w.Client = push.Client()
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			op, cancel := context.WithTimeout(ctx, 25*time.Second)
			w.tick(op)
			cancel()
		}
	}
}
func (w *PushWorker) tick(ctx context.Context) {
	if w.DB.QueuePush(ctx) != nil {
		return
	}
	j, err := w.DB.ClaimPush(ctx)
	if err != nil {
		return
	}
	code, skip := 0, !j.Eligible || j.Attempts > 5
	if !skip {
		public, encrypted, e := w.DB.PushIdentity(ctx, "", nil)
		if e == nil {
			private, e := w.Box.Open(encrypted, "push:vapid")
			defer clear(private)
			raw, e2 := w.Box.Open(j.Encrypted, "push-subscription:"+j.SubscriptionID)
			defer clear(raw)
			var sub webpush.Subscription
			if e == nil && e2 == nil && json.Unmarshal(raw, &sub) == nil {
				ttl := 3600
				if j.Kind == "outage" || j.Kind == "management" {
					ttl = 300
				}
				code, _ = push.Send(ctx, w.Client, sub, string(private), public, w.Origin, j.NotificationID, j.Kind, j.Severity, min(ttl, max(1, int(time.Until(j.Expires).Seconds()))))
			}
		}
	}
	save, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.DB.FinishPush(save, j, code, skip)
}
