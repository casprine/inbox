// Package retention holds the user's privacy preferences, the daily prune job and the
// "delete my stored mail" flow.
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/casprine/inbox/server/internal/store"
)

const (
	settingPrefs = "prefs"
	// MinRetentionDays is the shortest retention the server accepts.
	MinRetentionDays = 7
	MaxRetentionDays = 3650
	maxSenders       = 2000
	DefaultPreview   = 2
)

// Prefs are the settings the Privacy and Appearance screens edit.
type Prefs struct {
	// RetentionDays: stored mail older than this is deleted every night; nil keeps it forever.
	RetentionDays *int `json:"retention_days"`
	// ImageAllowSenders are senders (address or @domain) whose remote images load without asking.
	ImageAllowSenders []string `json:"image_allow_senders"`
	// PreviewLines is how many lines of each message the list previews (1-4).
	PreviewLines int `json:"preview_lines"`
}

// Defaults is what a fresh server returns.
func Defaults() Prefs {
	return Prefs{ImageAllowSenders: []string{}, PreviewLines: DefaultPreview}
}

// Load reads the stored prefs, filling defaults for anything missing.
func Load(ctx context.Context, q *store.Store) (Prefs, error) {
	p := Defaults()
	raw, err := q.GetSetting(ctx, settingPrefs)
	if errors.Is(err, store.ErrNotFound) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Defaults(), nil
	}
	if p.ImageAllowSenders == nil {
		p.ImageAllowSenders = []string{}
	}
	if p.PreviewLines < 1 || p.PreviewLines > 4 {
		p.PreviewLines = DefaultPreview
	}
	return p, nil
}

// Patch is a partial update: nil fields are unchanged. RetentionDays uses a pointer to a pointer
// so JSON null ("keep forever") is different from an omitted field.
type Patch struct {
	RetentionDays     **int     `json:"-"`
	ImageAllowSenders *[]string `json:"image_allow_senders"`
	PreviewLines      *int      `json:"preview_lines"`
}

// UnmarshalJSON distinguishes retention_days: null from a missing key.
func (p *Patch) UnmarshalJSON(b []byte) error {
	type plain Patch
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for k := range raw {
		switch k {
		case "retention_days", "image_allow_senders", "preview_lines":
		default:
			return fmt.Errorf("unknown field %q", k)
		}
	}
	var pl plain
	if err := json.Unmarshal(b, &pl); err != nil {
		return err
	}
	*p = Patch(pl)
	if v, ok := raw["retention_days"]; ok {
		var d *int
		if err := json.Unmarshal(v, &d); err != nil {
			return fmt.Errorf("retention_days: %w", err)
		}
		p.RetentionDays = &d
	}
	return nil
}

// FieldError names the offending field.
type FieldError struct{ Field, Message string }

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// Apply validates the patch and returns the new prefs.
func (p Patch) Apply(cur Prefs) (Prefs, error) {
	if p.RetentionDays != nil {
		d := *p.RetentionDays
		if d != nil && (*d < MinRetentionDays || *d > MaxRetentionDays) {
			return cur, FieldError{"retention_days", fmt.Sprintf("must be null (keep forever) or %d to %d days", MinRetentionDays, MaxRetentionDays)}
		}
		cur.RetentionDays = d
	}
	if p.PreviewLines != nil {
		if *p.PreviewLines < 1 || *p.PreviewLines > 4 {
			return cur, FieldError{"preview_lines", "must be 1 to 4"}
		}
		cur.PreviewLines = *p.PreviewLines
	}
	if p.ImageAllowSenders != nil {
		seen := map[string]bool{}
		out := []string{}
		for _, s := range *p.ImageAllowSenders {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "" || len(s) > 254 || strings.ContainsAny(s, " \t\r\n,;<>\"") || strings.Count(s, "@") > 1 || !strings.Contains(s, ".") && !strings.Contains(s, "@") {
				return cur, FieldError{"image_allow_senders", fmt.Sprintf("%q is not an address or @domain", s)}
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		if len(out) > maxSenders {
			return cur, FieldError{"image_allow_senders", fmt.Sprintf("at most %d senders", maxSenders)}
		}
		sort.Strings(out)
		cur.ImageAllowSenders = out
	}
	return cur, nil
}

// Save stores prefs.
func Save(ctx context.Context, q *store.Store, p Prefs) error {
	return q.SetSetting(ctx, settingPrefs, p)
}
