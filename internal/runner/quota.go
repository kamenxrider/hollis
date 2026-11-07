package runner

import (
	"context"
	"errors"
	"time"
)

// PCCQuota is advisory metadata, never evidence that inference will succeed.
// Unknown, absent and malformed readings cannot prevent a request.
type PCCQuota struct {
	Status    string `json:"status"`
	ResetDate string `json:"reset_date,omitempty"`
}

func (q PCCQuota) normalized() PCCQuota {
	switch q.Status {
	case "available", "approaching", "limit_reached":
	default:
		return PCCQuota{Status: "unknown"}
	}
	if q.ResetDate != "" {
		if date, err := time.Parse(time.RFC3339, q.ResetDate); err == nil {
			q.ResetDate = date.UTC().Format(time.RFC3339)
		} else {
			q.ResetDate = ""
		}
	}
	return q
}

// ReadPCCQuota performs a non-generative, bounded status request. No reading is
// cached: it is checked immediately before each Cloud-family Shortcut run.
func ReadPCCQuota(ctx context.Context, probe NativeStatusRunner) PCCQuota {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if probe != nil {
		status, err := probe.NativeStatus(ctx)
		if err == nil && ctx.Err() == nil && status.Capabilities != nil && status.Capabilities.PCCQuota != nil {
			return status.Capabilities.PCCQuota.normalized()
		}
	}
	return PCCQuota{Status: "unknown"}
}

func (q PCCQuota) LimitMessage() string {
	message := "Apple reports the Private Cloud Compute limit is reached; try again later"
	if q = q.normalized(); q.ResetDate != "" {
		message += " (reset: " + q.ResetDate + ")"
	}
	return message
}

func (r *ShortcutRunner) quotaPreflight(ctx context.Context, model Model) error {
	if model != ModelCloud && model != ModelCloudPro {
		return nil
	}
	quota := PCCQuota{Status: "unknown"}
	if r.quotaReader != nil {
		quota = r.quotaReader(ctx).normalized()
	} else if r.ShortcutsPath == DefaultShortcutsPath {
		quota = ReadPCCQuota(ctx, NewNative())
	}
	if quota.Status != "limit_reached" {
		return nil
	}
	return &Error{Kind: KindRateLimited, ExitCode: -1, PCCQuota: &quota, Err: errors.New(quota.LimitMessage())}
}
